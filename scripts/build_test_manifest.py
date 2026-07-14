#!/usr/bin/env python3
"""Build a deterministic inventory of the frozen Jasmin test suite."""

from __future__ import annotations

import argparse
import ast
import csv
import io
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
TEST_ROOT = ROOT / "tests"
OUTPUT = ROOT / "spec" / "compatibility" / "TEST_MANIFEST.csv"


def classify(path: Path, name: str) -> tuple[str, str]:
    rel = path.relative_to(ROOT).as_posix().lower()
    token = f"{rel}::{name.lower()}"
    if "/protocols/http/" in rel:
        return "HTTP_REST", "external-contract"
    if "/protocols/smpp/" in rel:
        if "_pb" in token:
            return "PB", "external-contract"
        return "SMPP", "external-contract"
    if "/protocols/cli/" in rel:
        return "JCLI", "external-contract"
    if "/queues/" in rel:
        return "AMQP", "infrastructure-integration"
    if "/redis/" in rel:
        return "REDIS", "infrastructure-integration"
    if "/interceptor/" in rel or "intercept" in token:
        return "INTERCEPT", "domain-behavior"
    if "bill" in token or "balance" in token or "quota" in token:
        return "BILLING", "domain-behavior"
    if "dlr" in token:
        return "DLR", "external-contract"
    if "thrower" in token or "deliver_sm" in token or "data_sm" in token:
        return "MO_CALLBACK", "external-contract"
    if "/routing/" in rel:
        return "ROUTING", "domain-behavior"
    if "/managers/" in rel:
        return "SMPPC", "infrastructure-integration"
    if "/encoding/" in rel:
        return "ENCODING", "domain-behavior"
    return "INTERNAL", "implementation-detail"


def rows() -> list[dict[str, str]]:
    result: list[dict[str, str]] = []
    for path in sorted(TEST_ROOT.rglob("*.py")):
        try:
            tree = ast.parse(path.read_text(encoding="utf-8"), filename=str(path))
        except SyntaxError as exc:
            raise SystemExit(f"Cannot parse {path}: {exc}") from exc
        parents: list[str] = []

        class Visitor(ast.NodeVisitor):
            def visit_ClassDef(self, node: ast.ClassDef) -> None:
                parents.append(node.name)
                self.generic_visit(node)
                parents.pop()

            def _function(self, node: ast.FunctionDef | ast.AsyncFunctionDef) -> None:
                if node.name.startswith("test"):
                    surface, kind = classify(path, node.name)
                    result.append(
                        {
                            "source": path.relative_to(ROOT).as_posix(),
                            "line": str(node.lineno),
                            "class": ".".join(parents),
                            "test": node.name,
                            "surface": surface,
                            "kind": kind,
                            "status": "UNREVIEWED",
                        }
                    )
                self.generic_visit(node)

            visit_FunctionDef = _function
            visit_AsyncFunctionDef = _function

        Visitor().visit(tree)
    return result


def render(items: list[dict[str, str]]) -> str:
    stream = io.StringIO(newline="")
    writer = csv.DictWriter(
        stream,
        fieldnames=["source", "line", "class", "test", "surface", "kind", "status"],
        lineterminator="\n",
    )
    writer.writeheader()
    writer.writerows(items)
    return stream.getvalue()


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--check", action="store_true", help="fail if the manifest is stale")
    args = parser.parse_args()
    items = rows()
    content = render(items)
    if args.check:
        if not OUTPUT.exists() or OUTPUT.read_text(encoding="utf-8") != content:
            raise SystemExit("TEST_MANIFEST.csv is stale; run scripts/build_test_manifest.py")
    else:
        OUTPUT.parent.mkdir(parents=True, exist_ok=True)
        OUTPUT.write_text(content, encoding="utf-8")
    files = len({item["source"] for item in items})
    surfaces = len({item["surface"] for item in items})
    print(f"tests={len(items)} files={files} surfaces={surfaces} status=ok")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
