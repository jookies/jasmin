#!/usr/bin/env python3
"""Pure parsing and validation model for the compatibility registry.

The module intentionally performs no CLI I/O and no repository mutation.  Callers
supply a repository root and, optionally, candidate evidence to validate.
"""
from __future__ import annotations

import csv
import hashlib
import json
import re
import subprocess
from collections import Counter, defaultdict
from dataclasses import dataclass
from pathlib import Path, PurePosixPath
from typing import Iterable, Mapping, Sequence

MATRIX_FILES = (
    "HTTP_MATRIX.md",
    "SMPP_MATRIX.md",
    "AMQP_REDIS_MATRIX.md",
    "ROUTING_BILLING_MATRIX.md",
    "PB_API_MATRIX.md",
    "JCLI_MATRIX.md",
    "CONFIG_OBSERVABILITY_DEPLOY_MATRIX.md",
)
STATUSES = frozenset((
    "INVENTORIED", "GO-PARTIAL", "MATCH", "GO-COMPLETE",
    "APPROVED_DEVIATION", "BLOCKED",
))
FINISHED_STATUSES = frozenset(("MATCH", "GO-COMPLETE", "APPROVED_DEVIATION"))
ID_RE = re.compile(r"^[A-Z]{1,3}-[0-9]{3}$", re.ASCII)
ID_LIKE_RE = re.compile(r"[A-Za-zΑ-ΩА-Яа-я]{1,4}[-‐‑‒–—−][0-9０-９]{3}")
TASK_RE = re.compile(r"^Task [0-9]+\.[0-9]+$")
SHA_RE = re.compile(r"^[0-9a-f]{40}$")
DIGEST_RE = re.compile(r"^[0-9a-f]{64}$")

OWNERSHIP_HEADER = ("row_id", "primary_macro", "primary_task", "dependency_tasks", "subcontract_boundary")
EDGES_HEADER = ("manifest_version", "flow_id", "edge_id", "from_boundary", "to_boundary", "required_row_ids", "required_post_state")
GRAPH_HEADER = ("manifest_version", "flow_id", "edge_id", "capability", "row_id", "current_status", "required_for_cutover", "dependencies", "oracle_surface", "go_packages", "owner", "tier_b_scope", "tier_c_required", "deviation_id", "approval_evidence", "rollback_invariant")
FIXTURE_HEADER = ("surface", "case_id", "contract_ids", "coverage", "notes")
MACRO_HEADER = ("scope", "mode", "requires_services", "commands", "required_tests", "cross_macro_gates")
BASELINE_FILE = "CONTRACT_ID_BASELINE.json"
EDGE_BASELINE_FILE = "CUTOVER_EDGE_BASELINE.json"
EVIDENCE_SCHEMA = "CANDIDATE_EVIDENCE.schema.json"
EVIDENCE_ATTESTOR = "CANDIDATE_EVIDENCE_ATTESTOR.pem"

KNOWN_MACROS = frozenset(f"MS-{i}" for i in range(1, 10))
TASK_TO_MACRO = {
    "Task 1.1": "MS-1", "Task 1.2": "MS-1", "Task 1.3": "MS-1", "Task 1.4": "MS-1", "Task 1.5": "MS-1",
    "Task 2.1": "MS-6", "Task 3.1": "MS-2", "Task 3.2": "MS-2", "Task 4.1": "MS-3", "Task 4.2": "MS-5",
    "Task 4.3": "MS-4", "Task 5.1": "MS-6", "Task 5.2": "MS-7", "Task 6.1": "MS-8", "Task 6.2": "MS-9", "Task 6.3": "MS-9",
}
EXPECTED_MACRO_COUNTS = {"MS-1": 39, "MS-2": 12, "MS-3": 7, "MS-4": 9, "MS-5": 31, "MS-6": 24, "MS-7": 18, "MS-8": 10, "MS-9": 34}
SCOPES = frozenset(("registry", "outbound-a", "outbound-b", "control", "dlr", "mo", "routing", "smpps", "pb", "jcli", "rest", "core-ops", "full"))
MODES = frozenset(("focused", "candidate", "release"))
FORMULA_PREFIXES = ("=", "+", "-", "@")
SCOPES_FOR_MACRO = {
    "MS-1": ("outbound-a", "outbound-b"), "MS-2": ("dlr",), "MS-3": ("mo",),
    "MS-4": ("smpps",), "MS-5": ("routing",), "MS-6": ("control",),
    "MS-7": ("jcli",), "MS-8": ("rest",), "MS-9": ("core-ops",),
}
MACRO_FOR_SCOPE = {
    scope: macro for macro, scopes in SCOPES_FOR_MACRO.items() for scope in scopes
}


class RegistryError(ValueError):
    pass


@dataclass(frozen=True)
class ContractRow:
    row_id: str
    status: str
    matrix: str
    line: int


@dataclass(frozen=True)
class Registry:
    rows: Mapping[str, ContractRow]

    @property
    def unfinished_ids(self) -> frozenset[str]:
        return frozenset(k for k, row in self.rows.items() if row.status not in FINISHED_STATUSES)

    @property
    def status_counts(self) -> Counter[str]:
        return Counter(row.status for row in self.rows.values())


def _read_clean(path: Path) -> str:
    data = path.read_bytes()
    if data.startswith(b"\xef\xbb\xbf"):
        raise RegistryError(f"{path}: UTF-8 BOM is forbidden")
    if b"\r" in data:
        raise RegistryError(f"{path}: CR/CRLF is forbidden; use LF")
    try:
        return data.decode("utf-8")
    except UnicodeDecodeError as exc:
        raise RegistryError(f"{path}: invalid UTF-8: {exc}") from exc


def _git_file(root: Path, revision: str, relative: str) -> str | None:
    """Read a tracked historical file; absence is valid for the introducing commit."""
    result = subprocess.run(
        ("git", "show", f"{revision}:{relative}"), cwd=root, text=True,
        stdout=subprocess.PIPE, stderr=subprocess.DEVNULL,
    )
    return result.stdout if result.returncode == 0 else None


def _historical_versions(root: Path, relative: str) -> tuple[str, ...]:
    if not (root / ".git").exists():
        return ()
    return tuple(
        content for revision in ("HEAD", "HEAD^")
        if (content := _git_file(root, revision, relative)) is not None
    )


def _ascii(value: str, where: str) -> None:
    if not value.isascii():
        raise RegistryError(f"{where}: non-ASCII/confusable text is forbidden: {value!r}")


def _markdown_cells(line: str) -> list[str] | None:
    stripped = line.strip()
    if not (stripped.startswith("|") and stripped.endswith("|")):
        return None
    return [cell.strip() for cell in stripped[1:-1].split("|")]


def _separator(cells: Sequence[str]) -> bool:
    return bool(cells) and all(re.fullmatch(r":?-{3,}:?", c) for c in cells)


def parse_matrix(path: Path) -> list[ContractRow]:
    rows: list[ContractRow] = []
    expected_columns: int | None = None
    for line_no, line in enumerate(_read_clean(path).splitlines(), 1):
        cells = _markdown_cells(line)
        stripped = line.strip()
        id_like = bool(ID_LIKE_RE.search(line) or ID_RE.search(line))
        if stripped.startswith("|") and id_like and cells is None:
            raise RegistryError(f"{path}:{line_no}: malformed ID-like Markdown row")
        if not cells:
            continue
        if cells[0].casefold() == "id" and cells[-1].casefold() == "status":
            expected_columns = len(cells)
            continue
        if _separator(cells):
            if expected_columns is not None and len(cells) != expected_columns:
                raise RegistryError(f"{path}:{line_no}: malformed Markdown column count")
            continue
        first = cells[0]
        last = cells[-1]
        if ID_RE.fullmatch(first):
            if expected_columns is None or len(cells) != expected_columns:
                raise RegistryError(f"{path}:{line_no}: malformed Markdown column count")
            _ascii(first, f"{path}:{line_no} ID")
            _ascii(last, f"{path}:{line_no} status")
            if last not in STATUSES:
                raise RegistryError(f"{path}:{line_no}: invalid or duplicate status {last!r}")
            if sum(cell in STATUSES for cell in cells) != 1:
                raise RegistryError(f"{path}:{line_no}: row must contain exactly one status")
            rows.append(ContractRow(first, last, path.name, line_no))
            continue
        if id_like or any(ID_RE.fullmatch(c) for c in cells[1:]):
            raise RegistryError(f"{path}:{line_no}: malformed ID-like Markdown row")
    if not rows:
        raise RegistryError(f"{path}: no contract rows parsed")
    return rows


def load_registry(root: Path) -> Registry:
    all_rows: dict[str, ContractRow] = {}
    compat = root / "spec" / "compatibility"
    for name in MATRIX_FILES:
        for row in parse_matrix(compat / name):
            old = all_rows.get(row.row_id)
            if old:
                raise RegistryError(f"duplicate contract ID {row.row_id}: {old.matrix}:{old.line}, {row.matrix}:{row.line}")
            all_rows[row.row_id] = row
    baseline_path = compat / BASELINE_FILE
    try:
        baseline = json.loads(_read_clean(baseline_path))
    except json.JSONDecodeError as exc:
        raise RegistryError(f"{baseline_path}: invalid baseline JSON: {exc}") from exc
    if not isinstance(baseline, dict) or set(baseline) != {"schema_version", "matrices"} or baseline["schema_version"] != "contract-id-baseline-v1":
        raise RegistryError(f"{baseline_path}: invalid baseline manifest")
    matrices = baseline["matrices"]
    if not isinstance(matrices, dict) or set(matrices) != set(MATRIX_FILES):
        raise RegistryError(f"{baseline_path}: matrix set mismatch")
    for name in MATRIX_FILES:
        expected = matrices[name]
        if not isinstance(expected, list) or any(not isinstance(v, str) or not ID_RE.fullmatch(v) for v in expected) or len(expected) != len(set(expected)):
            raise RegistryError(f"{baseline_path}: malformed/duplicate IDs for {name}")
        actual = sorted(row.row_id for row in all_rows.values() if row.matrix == name)
        if actual != sorted(expected):
            raise RegistryError(f"contract ID baseline mismatch for {name}: missing={sorted(set(expected)-set(actual))}, extra={sorted(set(actual)-set(expected))}")
    relative = f"spec/compatibility/{BASELINE_FILE}"
    current_ids = {name: frozenset(matrices[name]) for name in MATRIX_FILES}
    for historical_text in _historical_versions(root, relative):
        try:
            historical_matrices = json.loads(historical_text)["matrices"]
        except (json.JSONDecodeError, KeyError, TypeError) as exc:
            raise RegistryError(f"historical {BASELINE_FILE} is malformed") from exc
        for name in MATRIX_FILES:
            removed = set(historical_matrices.get(name, ())) - current_ids[name]
            if removed:
                raise RegistryError(
                    f"contract ID baseline is append-only; removed historical IDs from {name}: {sorted(removed)}"
                )
    return Registry(all_rows)


def read_csv_strict(path: Path, header: Sequence[str]) -> list[dict[str, str]]:
    text = _read_clean(path)
    reader = csv.DictReader(text.splitlines())
    if tuple(reader.fieldnames or ()) != tuple(header):
        raise RegistryError(f"{path}: expected exact CSV header {','.join(header)}")
    rows: list[dict[str, str]] = []
    for line_no, row in enumerate(reader, 2):
        if None in row or any(v is None for v in row.values()):
            raise RegistryError(f"{path}:{line_no}: malformed CSV column count")
        clean = {str(k): str(v).strip() for k, v in row.items()}
        if not any(clean.values()):
            raise RegistryError(f"{path}:{line_no}: blank CSV row")
        for column, value in clean.items():
            if value.startswith(FORMULA_PREFIXES):
                raise RegistryError(f"{path}:{line_no} {column}: CSV formula prefix forbidden")
        rows.append(clean)
    return rows


def split_semicolon(value: str, where: str, *, allow_empty: bool = True) -> tuple[str, ...]:
    if not value:
        if allow_empty:
            return ()
        raise RegistryError(f"{where}: semicolon list may not be empty")
    parts = tuple(part.strip() for part in value.split(";"))
    if any(not part for part in parts) or len(parts) != len(set(parts)):
        raise RegistryError(f"{where}: malformed/duplicate semicolon list")
    return parts


def _safe_repo_path(root: Path, value: str, where: str, *, must_exist: bool = False) -> None:
    if not value:
        return
    _ascii(value, where)
    if "\\" in value or re.match(r"^[A-Za-z]:", value) or value.startswith("//"):
        raise RegistryError(f"{where}: Windows/UNC/backslash paths are forbidden")
    p = PurePosixPath(value)
    if p.is_absolute() or ".." in p.parts or "." in p.parts:
        raise RegistryError(f"{where}: path must be normalized repository-relative")
    root_resolved = root.resolve()
    candidate = (root_resolved / p).resolve(strict=False)
    try:
        candidate.relative_to(root_resolved)
    except ValueError as exc:
        raise RegistryError(f"{where}: path escapes repository root") from exc
    if must_exist and not candidate.is_file():
        raise RegistryError(f"{where}: evidence file does not exist: {value}")


def validate_fixture_coverage(root: Path, registry: Registry) -> None:
    path = root / "spec/compatibility/FIXTURE_COVERAGE.csv"
    rows = read_csv_strict(path, FIXTURE_HEADER)
    covered: set[str] = set()
    case_keys: set[tuple[str, str]] = set()
    for n, row in enumerate(rows, 2):
        key = (row["surface"], row["case_id"])
        if key in case_keys:
            raise RegistryError(f"{path}:{n}: duplicate surface/case_id")
        case_keys.add(key)
        ids = split_semicolon(row["contract_ids"], f"{path}:{n} contract_ids", allow_empty=False)
        for row_id in ids:
            _ascii(row_id, f"{path}:{n} contract ID")
            if not ID_RE.fullmatch(row_id) or row_id not in registry.rows:
                raise RegistryError(f"{path}:{n}: unknown contract ID {row_id}")
            covered.add(row_id)
    missing = sorted(row_id for row_id, contract in registry.rows.items() if contract.status == "GO-PARTIAL" and row_id not in covered)
    if missing:
        raise RegistryError(f"GO-PARTIAL rows without executable fixture mapping: {','.join(missing)}")


def validate_ownership(root: Path, registry: Registry) -> list[dict[str, str]]:
    path = root / "spec/compatibility/ROW_OWNERSHIP.csv"
    rows = read_csv_strict(path, OWNERSHIP_HEADER)
    by_id: dict[str, dict[str, str]] = {}
    counts: Counter[str] = Counter()
    for n, row in enumerate(rows, 2):
        row_id = row["row_id"]
        if row_id in by_id:
            raise RegistryError(f"{path}:{n}: duplicate primary owner for {row_id}")
        if row_id not in registry.unfinished_ids:
            raise RegistryError(f"{path}:{n}: ownership must contain unfinished IDs only: {row_id}")
        macro, task = row["primary_macro"], row["primary_task"]
        if macro not in KNOWN_MACROS or task not in TASK_TO_MACRO:
            raise RegistryError(f"{path}:{n}: unknown macro/task {macro}/{task}")
        if TASK_TO_MACRO[task] != macro:
            raise RegistryError(f"{path}:{n}: task {task} belongs to {TASK_TO_MACRO[task]}, not {macro}")
        deps = split_semicolon(row["dependency_tasks"], f"{path}:{n} dependency_tasks")
        if task in deps:
            raise RegistryError(f"{path}:{n}: primary task may not depend on itself")
        if any(dep not in TASK_TO_MACRO for dep in deps):
            raise RegistryError(f"{path}:{n}: unknown dependency task")
        if bool(deps) != bool(row["subcontract_boundary"]):
            raise RegistryError(f"{path}:{n}: dependencies and subcontract_boundary must be provided together")
        by_id[row_id] = row
        counts[macro] += 1
    missing = sorted(registry.unfinished_ids - by_id.keys())
    extra = sorted(by_id.keys() - registry.unfinished_ids)
    if missing or extra:
        raise RegistryError(f"ownership set mismatch: missing={missing}, extra={extra}")
    if dict(sorted(counts.items())) != EXPECTED_MACRO_COUNTS:
        raise RegistryError(f"macro counts mismatch: got {dict(sorted(counts.items()))}, expected {EXPECTED_MACRO_COUNTS}")
    macro_dependency_closure(rows)
    return rows


def macro_dependency_closure(ownership: Sequence[Mapping[str, str]]) -> dict[str, frozenset[str]]:
    """Return transitive macro prerequisites and reject dependency cycles."""
    direct: dict[str, set[str]] = {macro: set() for macro in KNOWN_MACROS}
    for row in ownership:
        macro = row["primary_macro"]
        for task in split_semicolon(row["dependency_tasks"], "ownership dependency"):
            dependency = TASK_TO_MACRO[task]
            if dependency != macro:
                direct[macro].add(dependency)
    visiting: set[str] = set()
    complete: dict[str, frozenset[str]] = {}

    def visit(macro: str, trail: tuple[str, ...]) -> frozenset[str]:
        if macro in visiting:
            raise RegistryError(f"macro dependency cycle: {' -> '.join(trail + (macro,))}")
        if macro in complete:
            return complete[macro]
        visiting.add(macro)
        closure: set[str] = set()
        for dependency in sorted(direct[macro]):
            closure.add(dependency)
            closure.update(visit(dependency, trail + (macro,)))
        visiting.remove(macro)
        complete[macro] = frozenset(closure)
        return complete[macro]

    for macro in sorted(KNOWN_MACROS):
        visit(macro, ())
    return complete


def validate_cutover(root: Path, registry: Registry) -> None:
    compat = root / "spec/compatibility"
    edges = read_csv_strict(compat / "CUTOVER_EDGES.csv", EDGES_HEADER)
    graph = read_csv_strict(compat / "CUTOVER_GRAPH.csv", GRAPH_HEADER)
    baseline_path = compat / EDGE_BASELINE_FILE
    try:
        edge_baseline = json.loads(_read_clean(baseline_path))
    except json.JSONDecodeError as exc:
        raise RegistryError(f"{baseline_path}: invalid JSON: {exc}") from exc
    if (not isinstance(edge_baseline, dict)
            or set(edge_baseline) != {"schema_version", "edges"}
            or edge_baseline["schema_version"] != "cutover-edge-baseline-v1"
            or not isinstance(edge_baseline["edges"], list)):
        raise RegistryError(f"{baseline_path}: invalid edge baseline manifest")
    canonical_edges = [dict(row) for row in edges]
    if edge_baseline["edges"] != canonical_edges:
        raise RegistryError("CUTOVER edge baseline differs from CUTOVER_EDGES.csv")
    current_rows = {json.dumps(row, sort_keys=True, separators=(",", ":")) for row in canonical_edges}
    relative = f"spec/compatibility/{EDGE_BASELINE_FILE}"
    for historical_text in _historical_versions(root, relative):
        try:
            historical_rows = {
                json.dumps(row, sort_keys=True, separators=(",", ":"))
                for row in json.loads(historical_text)["edges"]
            }
        except (json.JSONDecodeError, KeyError, TypeError) as exc:
            raise RegistryError(f"historical {EDGE_BASELINE_FILE} is malformed") from exc
        if historical_rows - current_rows:
            raise RegistryError("CUTOVER edge baseline is append-only; historical edge rows were removed or changed")
    edge_keys: set[tuple[str, str, str]] = set()
    expected_required: dict[tuple[str, str, str], set[str]] = {}
    flows: set[str] = set()
    for n, row in enumerate(edges, 2):
        key = (row["manifest_version"], row["flow_id"], row["edge_id"])
        if key in edge_keys:
            raise RegistryError(f"CUTOVER_EDGES.csv:{n}: duplicate edge {key}")
        edge_keys.add(key); flows.add(row["flow_id"])
        ids = set(split_semicolon(row["required_row_ids"], f"CUTOVER_EDGES.csv:{n} required_row_ids", allow_empty=False))
        unknown = ids - registry.rows.keys()
        if unknown:
            raise RegistryError(f"CUTOVER_EDGES.csv:{n}: unknown required IDs {sorted(unknown)}")
        try:
            post = json.loads(row["required_post_state"])
        except json.JSONDecodeError as exc:
            raise RegistryError(f"CUTOVER_EDGES.csv:{n}: required_post_state is not JSON: {exc}") from exc
        if not isinstance(post, dict) or not post:
            raise RegistryError(f"CUTOVER_EDGES.csv:{n}: required_post_state must be a non-empty JSON object")
        expected_required[key] = ids
    if flows != {"outbound-mt", "dlr", "mo", "smpps-submit"}:
        raise RegistryError(f"CUTOVER_EDGES must cover all four Release A flows, got {sorted(flows)}")

    graph_edges: set[tuple[str, str, str]] = set()
    graph_required: dict[tuple[str, str, str], set[str]] = defaultdict(set)
    natural: set[tuple[str, str, str, str]] = set()
    full: set[tuple[str, ...]] = set()
    for n, row in enumerate(graph, 2):
        key = (row["manifest_version"], row["flow_id"], row["edge_id"])
        graph_edges.add(key)
        nat = key + (row["row_id"],)
        if nat in natural or tuple(row[h] for h in GRAPH_HEADER) in full:
            raise RegistryError(f"CUTOVER_GRAPH.csv:{n}: duplicate graph tuple")
        natural.add(nat); full.add(tuple(row[h] for h in GRAPH_HEADER))
        row_id = row["row_id"]
        if row_id not in registry.rows:
            raise RegistryError(f"CUTOVER_GRAPH.csv:{n}: unknown row ID {row_id}")
        if row["current_status"] != registry.rows[row_id].status:
            raise RegistryError(f"CUTOVER_GRAPH.csv:{n}: stale status for {row_id}")
        if row["required_for_cutover"] not in {"yes", "no"} or row["tier_c_required"] not in {"yes", "no"}:
            raise RegistryError(f"CUTOVER_GRAPH.csv:{n}: yes/no field invalid")
        dependencies = split_semicolon(row["dependencies"], f"CUTOVER_GRAPH.csv:{n} dependencies")
        if set(dependencies) - registry.rows.keys():
            raise RegistryError(f"CUTOVER_GRAPH.csv:{n}: unknown dependency row ID")
        _safe_repo_path(root, row["oracle_surface"], f"CUTOVER_GRAPH.csv:{n} oracle_surface")
        for package in split_semicolon(row["go_packages"], f"CUTOVER_GRAPH.csv:{n} go_packages"):
            _safe_repo_path(root, package, f"CUTOVER_GRAPH.csv:{n} go_packages")
        _safe_repo_path(root, row["approval_evidence"], f"CUTOVER_GRAPH.csv:{n} approval_evidence", must_exist=row["current_status"] == "APPROVED_DEVIATION")
        if row["current_status"] == "APPROVED_DEVIATION":
            if not row["deviation_id"] or not row["rollback_invariant"]:
                raise RegistryError(f"CUTOVER_GRAPH.csv:{n}: approved deviation lacks ID/rollback invariant")
        elif row["deviation_id"] or row["approval_evidence"]:
            raise RegistryError(f"CUTOVER_GRAPH.csv:{n}: deviation fields set for non-deviation")
        if row["required_for_cutover"] == "yes":
            graph_required[key].add(row_id)
    if graph_edges != edge_keys:
        raise RegistryError(f"cutover edge set mismatch: missing={sorted(edge_keys-graph_edges)}, extra={sorted(graph_edges-edge_keys)}")
    for key in edge_keys:
        if graph_required[key] != expected_required[key]:
            raise RegistryError(f"required row set mismatch for {key}: graph={sorted(graph_required[key])}, edges={sorted(expected_required[key])}")


def validate_macro_matrix(root: Path, ownership: Sequence[Mapping[str, str]]) -> list[dict[str, str]]:
    path = root / "spec/compatibility/GO_MACRO_TESTS.csv"
    rows = read_csv_strict(path, MACRO_HEADER)
    closure = macro_dependency_closure(ownership)
    keys: set[tuple[str, str]] = set()
    for n, row in enumerate(rows, 2):
        key = (row["scope"], row["mode"])
        if key in keys or key[0] not in SCOPES or key[1] not in MODES:
            raise RegistryError(f"{path}:{n}: duplicate/unknown scope-mode {key}")
        keys.add(key)
        if row["requires_services"] not in {"none", "rabbitmq-redis", "postgres-rabbitmq-redis-smsc"}:
            raise RegistryError(f"{path}:{n}: invalid requires_services")
        for command in split_semicolon(row["commands"], f"{path}:{n} commands"):
            if command.startswith(FORMULA_PREFIXES) or ".." in PurePosixPath(command).parts:
                raise RegistryError(f"{path}:{n}: unsafe command")
        split_semicolon(row["required_tests"], f"{path}:{n} required_tests")
        gates = frozenset(split_semicolon(row["cross_macro_gates"], f"{path}:{n} cross_macro_gates"))
        expected_gates = closure[MACRO_FOR_SCOPE[key[0]]] if key[0] in MACRO_FOR_SCOPE else frozenset()
        if gates != expected_gates:
            raise RegistryError(f"{path}:{n}: cross_macro_gates must equal transitive prerequisites: got={sorted(gates)}, expected={sorted(expected_gates)}")
    expected = {(scope, mode) for scope in SCOPES for mode in MODES}
    if keys != expected:
        raise RegistryError(f"GO_MACRO_TESTS scope/mode matrix incomplete: missing={sorted(expected-keys)}")
    return rows


def _git(root: Path, *args: str) -> str:
    return subprocess.run(("git", *args), cwd=root, check=True, text=True, stdout=subprocess.PIPE).stdout.strip()


def _clean_candidate_identity(root: Path) -> tuple[str, str]:
    status = _git(root, "status", "--porcelain=v1", "--untracked-files=all")
    if status:
        raise RegistryError("candidate evidence requires a clean index/worktree with no untracked files")
    head = _git(root, "rev-parse", "HEAD")
    tree = _git(root, "write-tree")
    if tree != _git(root, "rev-parse", "HEAD^{tree}"):
        raise RegistryError("candidate index tree differs from HEAD")
    return head, tree


def parse_framework_output(data: bytes, required_tests: Sequence[str]) -> dict[str, int]:
    """Parse runner-owned unittest summaries and `go test -json` events."""
    try:
        text = data.decode("utf-8")
    except UnicodeDecodeError as exc:
        raise RegistryError(f"runner output is not UTF-8: {exc}") from exc
    tests = skipped = failed = 0
    observed: set[str] = set()
    go_started: set[str] = set()
    go_terminal: set[str] = set()
    saw_framework = False
    for raw in text.splitlines():
        try:
            event = json.loads(raw)
        except json.JSONDecodeError:
            if "[no test files]" in raw:
                raise RegistryError("go test reported [no test files]")
            continue
        if not isinstance(event, dict):
            continue
        if event.get("framework") == "python-unittest":
            saw_framework = True
            names = event.get("test_names")
            values = (event.get("tests"), event.get("skipped"), event.get("failed"))
            if any(isinstance(v, bool) or not isinstance(v, int) or v < 0 for v in values) or not isinstance(names, list) or any(not isinstance(v, str) for v in names):
                raise RegistryError("malformed python-unittest result")
            if values[0] != len(names):
                raise RegistryError("python-unittest count/name mismatch")
            test_count = values[0] if isinstance(values[0], int) else 0
            skip_count = values[1] if isinstance(values[1], int) else 0
            fail_count = values[2] if isinstance(values[2], int) else 0
            tests += test_count; skipped += skip_count; failed += fail_count
            observed.update(names)
        elif isinstance(event.get("Action"), str) and isinstance(event.get("Package"), str) and event["Package"]:
            saw_framework = True
            action = event["Action"]
            test = event.get("Test")
            if test is None:
                if action == "fail":
                    failed += 1
                continue
            if not isinstance(test, str) or not test:
                raise RegistryError("malformed Go test event")
            name = f'{event["Package"]}::{test}'
            if action == "run":
                if name in go_started:
                    failed += 1
                go_started.add(name)
            elif action in {"pass", "fail", "skip"}:
                if name not in go_started or name in go_terminal:
                    failed += 1
                    continue
                go_terminal.add(name)
                if action == "pass":
                    observed.add(name)
                elif action == "skip":
                    skipped += 1
                else:
                    failed += 1
    completed_go = go_started & go_terminal
    tests += len(completed_go)
    if go_started - go_terminal:
        failed += len(go_started - go_terminal)
    if not saw_framework or tests <= 0:
        raise RegistryError("runner output contains zero executed framework tests")
    for required in required_tests:
        if required.startswith("Test"):
            raise RegistryError(f"mandatory Go test must be package-qualified with '::': {required}")
        if "::" in required:
            present = any(name == required or name.startswith(required + "/") for name in observed)
        else:
            present = any(name == required or name.startswith(required + ".") for name in observed)
        if not present:
            raise RegistryError(f"mandatory test was not executed: {required}")
    return {"tests": tests, "skipped": skipped, "failed": failed}


def validate_evidence(root: Path, evidence_dir: Path, ownership: Sequence[Mapping[str, str]], *, scope: str | None = None, mode: str | None = None, trusted_public_key: Path | None = None) -> None:
    try:
        from jsonschema import Draft202012Validator
    except ImportError as exc:
        raise RegistryError(
            "candidate evidence validation requires jsonschema; install compat/requirements-tools.txt"
        ) from exc
    root = root.resolve()
    if evidence_dir.is_symlink():
        raise RegistryError("evidence directory symlinks are forbidden")
    evidence_dir = evidence_dir.resolve()
    try:
        evidence_dir.relative_to(root)
    except ValueError:
        pass
    else:
        raise RegistryError("evidence directory must be outside the repository")
    marker = evidence_dir / ".runner-origin"
    if not evidence_dir.is_dir() or marker.is_symlink() or not marker.is_file():
        raise RegistryError("evidence must be atomically generated by run_go_macro_tests.sh in a fresh directory")
    head, tree = _clean_candidate_identity(root)
    schema_path = root / "spec/compatibility" / EVIDENCE_SCHEMA
    public_key_path = trusted_public_key or (root / "spec/compatibility" / EVIDENCE_ATTESTOR)
    if public_key_path.is_symlink() or not public_key_path.is_file():
        raise RegistryError("candidate evidence trusted public key is missing or unsafe")
    public_key = public_key_path.resolve()
    schema = json.loads(_read_clean(schema_path))
    Draft202012Validator.check_schema(schema)
    validator = Draft202012Validator(schema)
    policy_rows = validate_macro_matrix(root, ownership)
    policy = {(r["scope"], r["mode"]): r for r in policy_rows}
    closure = macro_dependency_closure(ownership)
    evidence: dict[tuple[str, str], dict[str, object]] = {}
    json_paths = sorted(evidence_dir.glob("*.json"))
    if not json_paths:
        raise RegistryError("evidence directory contains no JSON evidence")
    for path in json_paths:
        if path.is_symlink() or not path.is_file():
            raise RegistryError(f"{path}: evidence must be a regular file")
        signature = path.with_suffix(path.suffix + ".sig")
        if signature.is_symlink() or not signature.is_file():
            raise RegistryError(f"{path}: detached attestation signature is missing or unsafe")
        verified = subprocess.run(
            ("openssl", "dgst", "-sha256", "-verify", str(public_key),
             "-signature", str(signature), str(path)),
            stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL,
        )
        if verified.returncode != 0:
            raise RegistryError(f"{path}: detached attestation signature is invalid")
        try:
            data = json.loads(_read_clean(path))
        except json.JSONDecodeError as exc:
            raise RegistryError(f"{path}: invalid evidence JSON: {exc}") from exc
        errors = sorted(validator.iter_errors(data), key=lambda error: list(error.absolute_path))
        if errors:
            pointer = "/" + "/".join(str(part) for part in errors[0].absolute_path)
            raise RegistryError(f"{path}{pointer}: schema validation failed: {errors[0].message}")
        results = data["results"]
        if any(isinstance(results[k], bool) or not isinstance(results[k], int) for k in ("tests", "skipped", "failed")):
            raise RegistryError(f"{path}: boolean/inexact integer result counts are forbidden")
        key = (data["scope"], data["mode"])
        if key in evidence:
            raise RegistryError(f"duplicate evidence scope/mode: {key}")
        if data["commit_sha"] != head or data["tree_hash"] != tree:
            raise RegistryError(f"{path}: stale candidate SHA/tree")
        row = policy[key]
        expected_commands = list(split_semicolon(row["commands"], "policy commands", allow_empty=False))
        expected_tests = list(split_semicolon(row["required_tests"], "policy required_tests", allow_empty=False))
        expected_gates = list(split_semicolon(row["cross_macro_gates"], "policy cross_macro_gates"))
        if data["commands"] != expected_commands or data["required_tests"] != expected_tests or data["cross_macro_gates"] != expected_gates:
            raise RegistryError(f"{path}: evidence policy differs from GO_MACRO_TESTS.csv")
        output_name = data["output_file"]
        if PurePosixPath(output_name).name != output_name or output_name.startswith(FORMULA_PREFIXES):
            raise RegistryError(f"{path}: unsafe output_file")
        output_file = evidence_dir / output_name
        if output_file.is_symlink() or not output_file.is_file() or hashlib.sha256(output_file.read_bytes()).hexdigest() != data["output_sha256"]:
            raise RegistryError(f"{path}: missing or digest-mismatched runner output")
        parsed = parse_framework_output(output_file.read_bytes(), expected_tests)
        if parsed != results or results["skipped"] != 0 or results["failed"] != 0:
            raise RegistryError(f"{path}: declared results differ from successful framework output")
        evidence[key] = data
    if scope and mode and (scope, mode) not in evidence:
        raise RegistryError(f"missing current candidate evidence for {scope}/{mode}")
    closure_file = evidence_dir / "CLOSE_MACROS"
    if closure_file.exists():
        if closure_file.is_symlink() or not closure_file.is_file():
            raise RegistryError("CLOSE_MACROS must be a regular file")
        closure_signature = evidence_dir / "CLOSE_MACROS.sig"
        if closure_signature.is_symlink() or not closure_signature.is_file():
            raise RegistryError("CLOSE_MACROS detached signature is missing or unsafe")
        verified = subprocess.run(
            ("openssl", "dgst", "-sha256", "-verify", str(public_key),
             "-signature", str(closure_signature), str(closure_file)),
            stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL,
        )
        if verified.returncode != 0:
            raise RegistryError("CLOSE_MACROS detached signature is invalid")
        macros = split_semicolon(_read_clean(closure_file).strip(), "CLOSE_MACROS", allow_empty=False)
        for macro in macros:
            if macro not in KNOWN_MACROS:
                raise RegistryError(f"unknown macro closure {macro}")
            needed = set(closure[macro]) | {macro}
            missing = [
                f"{candidate_scope}/candidate"
                for needed_macro in sorted(needed)
                for candidate_scope in SCOPES_FOR_MACRO[needed_macro]
                if (candidate_scope, "candidate") not in evidence
            ]
            if missing:
                raise RegistryError(f"macro {macro} lacks transitive cross-macro candidate gates: {missing}")


def validate_repository(root: Path, evidence_dir: Path | None = None, *, scope: str | None = None, mode: str | None = None) -> Registry:
    registry = load_registry(root)
    validate_fixture_coverage(root, registry)
    ownership = validate_ownership(root, registry)
    validate_cutover(root, registry)
    validate_macro_matrix(root, ownership)
    if evidence_dir is not None:
        validate_evidence(root, evidence_dir, ownership, scope=scope, mode=mode)
    return registry
