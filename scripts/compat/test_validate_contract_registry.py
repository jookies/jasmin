from __future__ import annotations

import csv
import hashlib
import json
import shutil
import subprocess
import tempfile
import unittest
from pathlib import Path

from scripts.compat.contract_registry import (
    EDGES_HEADER, FINISHED_STATUSES, GRAPH_HEADER, MATRIX_FILES, RegistryError,
    load_registry, parse_framework_output, parse_matrix, read_csv_strict, validate_evidence,
    macro_dependency_closure, validate_ownership, validate_repository,
)

ROOT = Path(__file__).resolve().parents[2]


class MatrixParserTests(unittest.TestCase):
    def matrix(self, text: str):
        with tempfile.TemporaryDirectory() as td:
            path = Path(td) / "M.md"
            path.write_bytes(text.encode("utf-8"))
            return parse_matrix(path)

    def test_headerless_smpp_block_and_blank_b_gap(self):
        rows = self.matrix("| ID | Area | Status |\n|---|---|---|\n| S-001 | framing | INVENTORIED |\n\n| SP-001 | submit | GO-PARTIAL |\n| B-007 | late | MATCH |\n\n| B-009 | timer | BLOCKED |\n")
        self.assertEqual([r.row_id for r in rows], ["S-001", "SP-001", "B-007", "B-009"])

    def test_rejects_malformed_id_like_row(self):
        with self.assertRaisesRegex(RegistryError, "malformed ID-like"):
            self.matrix("| ID | Area | Status |\n|---|---|---|\n| Operations | MATCH | H-012 |\n")
        with self.assertRaisesRegex(RegistryError, "malformed ID-like"):
            self.matrix("| ID | Area | Status |\n|---|---|---|\n| A-011 | queue | INVENTORIED\n")

    def test_rejects_duplicate_or_unknown_status(self):
        with self.assertRaises(RegistryError):
            self.matrix("| X-001 | x | MATCH | GO-PARTIAL |\n")
        with self.assertRaises(RegistryError):
            self.matrix("| X-001 | x | IMPLEMENTED |\n")

    def test_rejects_unicode_confusable_bom_and_crlf(self):
        for payload in ("| А-001 | x | MATCH |\n".encode(), b"\xef\xbb\xbf| A-001 | x | MATCH |\n", b"| A-001 | x | MATCH |\r\n"):
            with self.subTest(payload=payload), tempfile.TemporaryDirectory() as td:
                path = Path(td) / "M.md"; path.write_bytes(payload)
                with self.assertRaises(RegistryError):
                    parse_matrix(path)

    def test_fixed_seven_matrices_and_unique_ids(self):
        registry = load_registry(ROOT)
        self.assertEqual(len(MATRIX_FILES), 7)
        self.assertEqual(len(registry.rows), len(set(registry.rows)))

    def test_contract_id_baseline_rejects_deleted_finished_row(self):
        with tempfile.TemporaryDirectory() as td:
            clone = Path(td) / "repo"
            shutil.copytree(ROOT, clone, ignore=shutil.ignore_patterns(".git", "__pycache__"))
            matrix = clone / "spec/compatibility/HTTP_MATRIX.md"
            lines = matrix.read_text().splitlines()
            matrix.write_text("\n".join(line for line in lines if "| H-001 |" not in line) + "\n")
            with self.assertRaisesRegex(RegistryError, "baseline mismatch"):
                load_registry(clone)


class StrictCSVTests(unittest.TestCase):
    def test_header_must_be_exact(self):
        with tempfile.TemporaryDirectory() as td:
            path = Path(td) / "x.csv"; path.write_text("b,a\n1,2\n")
            with self.assertRaisesRegex(RegistryError, "exact CSV header"):
                read_csv_strict(path, ("a", "b"))


class FrameworkEvidenceTests(unittest.TestCase):
    def test_go_required_test_is_package_qualified(self):
        output = (b'{"Action":"run","Package":"example.invalid/right","Test":"TestGolden"}\n'
                  b'{"Action":"pass","Package":"example.invalid/right","Test":"TestGolden"}\n')
        self.assertEqual(1, parse_framework_output(output, ["example.invalid/right::TestGolden"])["tests"])
        with self.assertRaisesRegex(RegistryError, "package-qualified"):
            parse_framework_output(output, ["TestGolden"])
        with self.assertRaisesRegex(RegistryError, "mandatory test was not executed"):
            parse_framework_output(output, ["example.invalid/wrong::TestGolden"])

    def test_go_package_failure_and_terminal_without_run_fail_closed(self):
        package_failure = (
            b'{"Action":"run","Package":"example/good","Test":"TestOK"}\n'
            b'{"Action":"pass","Package":"example/good","Test":"TestOK"}\n'
            b'{"Action":"fail","Package":"example/compile-broken","Elapsed":0.01}\n'
        )
        parsed = parse_framework_output(package_failure, ["example/good::TestOK"])
        self.assertGreater(parsed["failed"], 0)
        terminal_only = b'{"Action":"pass","Package":"example/good","Test":"TestOK"}\n'
        with self.assertRaisesRegex(RegistryError, "zero executed"):
            parse_framework_output(terminal_only, ["example/good::TestOK"])


class RepositoryRegistryTests(unittest.TestCase):
    def make_clean_clone(self, parent: Path) -> tuple[Path, Path]:
        clone = parent / "repo"
        shutil.copytree(ROOT, clone, ignore=shutil.ignore_patterns(".git", "__pycache__"))
        private_key = parent / "attestor-private.pem"
        subprocess.run([
            "openssl", "genpkey", "-algorithm", "RSA", "-pkeyopt", "rsa_keygen_bits:2048",
            "-out", str(private_key),
        ], check=True, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        subprocess.run([
            "openssl", "pkey", "-in", str(private_key), "-pubout",
            "-out", str(clone / "spec/compatibility/CANDIDATE_EVIDENCE_ATTESTOR.pem"),
        ], check=True, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        subprocess.run(["git", "init", "-q"], cwd=clone, check=True)
        subprocess.run(["git", "config", "user.name", "Wave0 Test"], cwd=clone, check=True)
        subprocess.run(["git", "config", "user.email", "wave0@example.invalid"], cwd=clone, check=True)
        subprocess.run(["git", "add", "-A"], cwd=clone, check=True)
        subprocess.run(["git", "commit", "-qm", "test candidate"], cwd=clone, check=True)
        return clone, private_key

    def test_repository_is_self_consistent(self):
        registry = validate_repository(ROOT)
        self.assertEqual(len(registry.rows), 205)
        self.assertEqual(len(registry.unfinished_ids), 184)
        self.assertEqual(sum(r.status in FINISHED_STATUSES for r in registry.rows.values()), 21)

    def test_cutover_headers_are_exact(self):
        read_csv_strict(ROOT / "spec/compatibility/CUTOVER_EDGES.csv", EDGES_HEADER)
        read_csv_strict(ROOT / "spec/compatibility/CUTOVER_GRAPH.csv", GRAPH_HEADER)

    def test_mutations_fail_closed(self):
        cases = {
            "unknown_fixture": ("spec/compatibility/FIXTURE_COVERAGE.csv", "H-012", "ZZ-999"),
            "stale_graph": ("spec/compatibility/CUTOVER_GRAPH.csv", ",MATCH,", ",BLOCKED,"),
            "duplicate_owner": ("spec/compatibility/ROW_OWNERSHIP.csv", None, None),
            "traversal": ("spec/compatibility/CUTOVER_GRAPH.csv", "spec/compatibility/HTTP_MATRIX.md", "../HTTP_MATRIX.md"),
            "windows_path": ("spec/compatibility/CUTOVER_GRAPH.csv", "spec/compatibility/HTTP_MATRIX.md", r"C:\\Windows\\system.ini"),
            "csv_formula": ("spec/compatibility/CUTOVER_GRAPH.csv", ",Integrator,", ",=HYPERLINK(x),"),
        }
        for name, (relative, old, new) in cases.items():
            with self.subTest(name=name), tempfile.TemporaryDirectory() as td:
                clone = Path(td) / "repo"
                shutil.copytree(ROOT, clone, ignore=shutil.ignore_patterns(".git", "__pycache__"))
                path = clone / relative
                if name == "duplicate_owner":
                    lines = path.read_text().splitlines(); path.write_text("\n".join(lines + [lines[1]]) + "\n")
                else:
                    text = path.read_text(); self.assertIn(old, text); path.write_text(text.replace(old, new, 1))
                with self.assertRaises((RegistryError, subprocess.CalledProcessError)):
                    validate_repository(clone)

    def test_graph_rejects_duplicate_row_with_changed_capability(self):
        with tempfile.TemporaryDirectory() as td:
            clone = Path(td) / "repo"
            shutil.copytree(ROOT, clone, ignore=shutil.ignore_patterns(".git", "__pycache__"))
            graph = clone / "spec/compatibility/CUTOVER_GRAPH.csv"
            lines = graph.read_text().splitlines()
            duplicate = lines[1].replace("public-http-to-canonical-submit", "different-capability", 1)
            graph.write_text("\n".join(lines + [duplicate]) + "\n")
            with self.assertRaisesRegex(RegistryError, "duplicate graph tuple"):
                validate_repository(clone)

    def test_macro_dependencies_are_transitive_and_cycles_fail(self):
        rows = [
            {"primary_macro": "MS-2", "dependency_tasks": "Task 1.1"},
            {"primary_macro": "MS-3", "dependency_tasks": "Task 3.1"},
        ]
        closure = macro_dependency_closure(rows)
        self.assertEqual(frozenset(("MS-1", "MS-2")), closure["MS-3"])
        cyclic = [
            {"primary_macro": "MS-2", "dependency_tasks": "Task 4.1"},
            {"primary_macro": "MS-3", "dependency_tasks": "Task 3.1"},
        ]
        with self.assertRaisesRegex(RegistryError, "dependency cycle"):
            macro_dependency_closure(cyclic)

    def test_simultaneous_contract_and_baseline_deletion_is_rejected(self):
        with tempfile.TemporaryDirectory() as td:
            clone, _ = self.make_clean_clone(Path(td))
            matrix = clone / "spec/compatibility/HTTP_MATRIX.md"
            matrix.write_text("\n".join(
                line for line in matrix.read_text().splitlines() if "| HE-006 |" not in line
            ) + "\n")
            baseline_path = clone / "spec/compatibility/CONTRACT_ID_BASELINE.json"
            baseline = json.loads(baseline_path.read_text())
            baseline["matrices"]["HTTP_MATRIX.md"].remove("HE-006")
            baseline_path.write_text(json.dumps(baseline, indent=2) + "\n")
            with self.assertRaisesRegex(RegistryError, "append-only"):
                load_registry(clone)

    def test_simultaneous_edge_graph_and_baseline_deletion_is_rejected(self):
        with tempfile.TemporaryDirectory() as td:
            clone, _ = self.make_clean_clone(Path(td))
            edges = clone / "spec/compatibility/CUTOVER_EDGES.csv"
            edges.write_text("\n".join(
                line for line in edges.read_text().splitlines()
                if ",outbound-mt,result-effects," not in line
            ) + "\n")
            graph = clone / "spec/compatibility/CUTOVER_GRAPH.csv"
            graph.write_text("\n".join(
                line for line in graph.read_text().splitlines()
                if ",outbound-mt,result-effects," not in line
            ) + "\n")
            baseline_path = clone / "spec/compatibility/CUTOVER_EDGE_BASELINE.json"
            baseline = json.loads(baseline_path.read_text())
            baseline["edges"] = [row for row in baseline["edges"] if row["edge_id"] != "result-effects"]
            baseline_path.write_text(json.dumps(baseline, indent=2) + "\n")
            with self.assertRaisesRegex(RegistryError, "append-only"):
                validate_repository(clone)

    def test_evidence_requires_valid_signature_and_rejects_stale_skip_zero_dirty_and_missing_gate(self):
        with tempfile.TemporaryDirectory() as td:
            temp = Path(td)
            clone, private_key = self.make_clean_clone(temp)
            registry = validate_repository(clone)
            self.assertEqual(184, len(registry.unfinished_ids))
            ownership = validate_ownership(clone, registry)
            head = subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=clone, text=True).strip()
            tree = subprocess.check_output(["git", "rev-parse", "HEAD^{tree}"], cwd=clone, text=True).strip()
            evidence = temp / "evidence"
            evidence.mkdir()
            (evidence / ".runner-origin").touch()
            with (clone / "spec/compatibility/GO_MACRO_TESTS.csv").open(newline="") as handle:
                policy = next(row for row in csv.DictReader(handle) if row["scope"] == "registry" and row["mode"] == "focused")
            test_name = "scripts.compat.test_validate_contract_registry.RepositoryRegistryTests.test_repository_is_self_consistent"
            output = (json.dumps({"framework": "python-unittest", "tests": 1, "skipped": 0,
                                  "failed": 0, "test_names": [test_name]}, separators=(",", ":")) + "\n").encode()
            base = {
                "schema_version": "candidate-evidence-v1", "commit_sha": head,
                "tree_hash": tree, "scope": "registry", "mode": "focused",
                "commands": policy["commands"].split(";"),
                "required_tests": policy["required_tests"].split(";"), "cross_macro_gates": [],
                "results": {"tests": 1, "skipped": 0, "failed": 0},
                "output_file": "registry.out", "output_sha256": hashlib.sha256(output).hexdigest(),
            }
            (evidence / "registry.out").write_bytes(output)
            evidence_link = temp / "evidence-link"
            evidence_link.symlink_to(evidence, target_is_directory=True)
            with self.assertRaisesRegex(RegistryError, "symlink"):
                validate_evidence(clone, evidence_link, ownership)
            marker = evidence / ".runner-origin"
            marker.unlink(); marker.symlink_to(evidence / "registry.out")
            with self.assertRaisesRegex(RegistryError, "atomically generated"):
                validate_evidence(clone, evidence, ownership)
            marker.unlink(); marker.touch()
            evidence_json = evidence / "registry.json"
            signature = evidence / "registry.json.sig"

            def write_signed(candidate: dict[str, object]) -> None:
                evidence_json.write_text(json.dumps(candidate, sort_keys=True, separators=(",", ":")) + "\n")
                subprocess.run([
                    "openssl", "dgst", "-sha256", "-sign", str(private_key),
                    "-out", str(signature), str(evidence_json),
                ], check=True, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)

            evidence_json.write_text(json.dumps(base))
            with self.assertRaisesRegex(RegistryError, "signature is missing"):
                validate_evidence(clone, evidence, ownership)
            write_signed(base)
            evidence_json.write_text(evidence_json.read_text() + " ")
            with self.assertRaisesRegex(RegistryError, "signature is invalid"):
                validate_evidence(clone, evidence, ownership)
            stale = dict(base); stale["commit_sha"] = "0" * 40
            write_signed(stale)
            with self.assertRaisesRegex(RegistryError, "stale"):
                validate_evidence(clone, evidence, ownership)
            for results in ({"tests": 0, "skipped": 0, "failed": 0}, {"tests": 1, "skipped": 1, "failed": 0}):
                candidate = dict(base); candidate["results"] = results
                write_signed(candidate)
                with self.assertRaises(RegistryError):
                    validate_evidence(clone, evidence, ownership)
            write_signed(base)
            close_file = evidence / "CLOSE_MACROS"
            close_signature = evidence / "CLOSE_MACROS.sig"
            close_file.write_text("MS-1\n")
            subprocess.run([
                "openssl", "dgst", "-sha256", "-sign", str(private_key),
                "-out", str(close_signature), str(close_file),
            ], check=True, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
            with self.assertRaisesRegex(RegistryError, "outbound-a/candidate") as caught:
                validate_evidence(clone, evidence, ownership)
            self.assertIn("outbound-b/candidate", str(caught.exception))
            close_file.unlink(); close_signature.unlink()
            dirty = clone / "spec/compatibility/SURFACES.md"
            dirty.write_text(dirty.read_text() + "\n")
            with self.assertRaisesRegex(RegistryError, "clean index/worktree"):
                validate_evidence(clone, evidence, ownership)


if __name__ == "__main__":
    unittest.main()
