from __future__ import annotations

import csv
import os
import stat
import subprocess
import sys
import tempfile
import time
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
WRAPPER = ROOT / "scripts/compat/run_go_macro_tests.sh"
HEADER = ("scope","mode","requires_services","commands","required_tests","cross_macro_gates")


class MacroWrapperTests(unittest.TestCase):
    def run_wrapper(self, *args, env=None):
        merged = os.environ.copy()
        merged.setdefault("GO_MACRO_TEST_PYTHON", sys.executable)
        merged.update(env or {})
        return subprocess.run([str(WRAPPER), *args], cwd=ROOT, env=merged, text=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE)

    def test_invalid_args_do_not_invoke_docker(self):
        with tempfile.TemporaryDirectory() as td:
            marker = Path(td) / "docker-called"
            fake = Path(td) / "docker"
            fake.write_text(f"#!/bin/sh\ntouch '{marker}'\nexit 99\n")
            fake.chmod(fake.stat().st_mode | stat.S_IXUSR)
            result = self.run_wrapper("bad", "focused", env={"GO_MACRO_TEST_DOCKER_BIN": str(fake)})
            self.assertEqual(64, result.returncode)
            self.assertFalse(marker.exists())

    def test_registry_focused_does_not_invoke_docker(self):
        with tempfile.TemporaryDirectory() as td:
            marker = Path(td) / "docker-called"
            fake = Path(td) / "docker"; fake.write_text(f"#!/bin/sh\ntouch '{marker}'\nexit 99\n"); fake.chmod(0o755)
            result = self.run_wrapper("registry", "focused", env={"GO_MACRO_TEST_DOCKER_BIN": str(fake)})
            self.assertEqual(0, result.returncode, result.stdout + result.stderr)
            self.assertIn('"skipped":0', result.stdout)
            self.assertFalse(marker.exists())

    def test_unconfigured_scope_fails_without_docker(self):
        with tempfile.TemporaryDirectory() as td:
            marker = Path(td) / "docker-called"; fake = Path(td) / "docker"
            fake.write_text(f"#!/bin/sh\ntouch '{marker}'\nexit 99\n"); fake.chmod(0o755)
            result = self.run_wrapper("outbound-a", "candidate", env={"GO_MACRO_TEST_DOCKER_BIN": str(fake)})
            self.assertEqual(78, result.returncode)
            self.assertIn("not yet configured", result.stderr)
            self.assertIn("PostgreSQL and SMSC", result.stderr)
            self.assertFalse(marker.exists())

    def test_evidence_target_requires_matching_private_key_before_tests(self):
        with tempfile.TemporaryDirectory() as td:
            target = Path(td) / "evidence"
            result = self.run_wrapper("registry", "focused", env={"GO_MACRO_EVIDENCE_DIR": str(target)})
            self.assertEqual(65, result.returncode, result.stdout + result.stderr)
            self.assertIn("GO_MACRO_ATTEST_PRIVATE_KEY", result.stderr)
            self.assertFalse(target.exists())

            wrong_key = Path(td) / "wrong.pem"
            subprocess.run([
                "openssl", "genpkey", "-algorithm", "RSA", "-pkeyopt", "rsa_keygen_bits:2048",
                "-out", str(wrong_key),
            ], check=True, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
            result = self.run_wrapper("registry", "focused", env={
                "GO_MACRO_EVIDENCE_DIR": str(target),
                "GO_MACRO_ATTEST_PRIVATE_KEY": str(wrong_key),
            })
            self.assertEqual(65, result.returncode, result.stdout + result.stderr)
            self.assertIn("does not match repository trust anchor", result.stderr)
            self.assertFalse(target.exists())

    def test_cleanup_failure_propagates(self):
        with tempfile.TemporaryDirectory() as td:
            td = Path(td); log = td / "docker.log"; fake = td / "docker"; fake_go = td / "go"
            fake.write_text(f'''#!/bin/sh\necho "$*" >> "{log}"\ncase "$*" in *" down "*) exit 23;; esac\nexit 0\n'''); fake.chmod(0o755)
            fake_go.write_text('''#!/bin/sh
printf '%s\n' '{"Action":"run","Package":"example.invalid/fake","Test":"TestCleanup"}'
printf '%s\n' '{"Action":"pass","Package":"example.invalid/fake","Test":"TestCleanup"}'
exit 0
'''); fake_go.chmod(0o755)
            matrix = td / "matrix.csv"
            with matrix.open("w", newline="") as f:
                w=csv.writer(f,lineterminator="\n"); w.writerow(HEADER); w.writerow(("outbound-a","focused","rabbitmq-redis","go test -json ./fake","example.invalid/fake::TestCleanup",""))
            result = self.run_wrapper("outbound-a", "focused", env={
                "GO_MACRO_TEST_DOCKER_BIN": str(fake), "GO_MACRO_TEST_MATRIX": str(matrix),
                "GO_MACRO_TEST_GO_BIN": str(fake_go),
                "PYTHON_PATH": os.environ.get("PYTHON_PATH", sys.executable),
            })
            self.assertEqual(23, result.returncode, result.stdout + result.stderr)
            text = log.read_text(); self.assertIn(" up -d rabbitmq redis", text); self.assertIn(" down --volumes --remove-orphans", text)

    def test_term_preserves_signal_status_and_cleans_up(self):
        with tempfile.TemporaryDirectory() as td:
            td = Path(td); log = td / "docker.log"; fake = td / "docker"
            fake.write_text(f'''#!/bin/sh
echo "$*" >> "{log}"
case "$*" in *" up -d "*) sleep 1;; esac
exit 0
'''); fake.chmod(0o755)
            matrix = td / "matrix.csv"
            with matrix.open("w", newline="") as handle:
                writer = csv.writer(handle, lineterminator="\n")
                writer.writerow(HEADER)
                writer.writerow(("outbound-a", "focused", "rabbitmq-redis", "go test -json ./fake", "example.invalid/fake::TestCleanup", ""))
            env = os.environ.copy()
            env.update({"GO_MACRO_TEST_PYTHON": sys.executable,
                        "GO_MACRO_TEST_DOCKER_BIN": str(fake),
                        "GO_MACRO_TEST_MATRIX": str(matrix),
                        "PYTHON_PATH": os.environ.get("PYTHON_PATH", sys.executable)})
            proc = subprocess.Popen([str(WRAPPER), "outbound-a", "focused"], cwd=ROOT, env=env,
                                    text=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE)
            deadline = time.monotonic() + 5
            while time.monotonic() < deadline:
                if log.exists() and " up -d rabbitmq redis" in log.read_text():
                    break
                time.sleep(0.02)
            else:
                proc.kill(); self.fail("wrapper never reached compose up")
            proc.terminate()
            stdout, stderr = proc.communicate(timeout=5)
            self.assertEqual(143, proc.returncode, stdout + stderr)
            self.assertIn(" down --volumes --remove-orphans", log.read_text())

    def test_go_skip_and_zero_tests_never_produce_evidence(self):
        payloads = {
            "skip": [
                '{"Action":"run","Package":"example.invalid/fake","Test":"TestMandatory"}',
                '{"Action":"skip","Package":"example.invalid/fake","Test":"TestMandatory"}',
            ],
            "zero": ['{"Action":"pass","Package":"example.invalid/fake"}'],
        }
        for name, events in payloads.items():
            with self.subTest(name=name), tempfile.TemporaryDirectory() as td:
                td = Path(td); fake_go = td / "go"; matrix = td / "matrix.csv"
                lines = ["#!/bin/sh"]
                lines.extend("printf '%s\\n' " + repr(event) for event in events)
                lines.append("exit 0")
                fake_go.write_text("\n".join(lines) + "\n"); fake_go.chmod(0o755)
                with matrix.open("w", newline="") as handle:
                    writer = csv.writer(handle, lineterminator="\n")
                    writer.writerow(HEADER)
                    writer.writerow(("registry", "focused", "none", "go test -json ./fake", "example.invalid/fake::TestMandatory", ""))
                result = self.run_wrapper("registry", "focused", env={
                    "GO_MACRO_TEST_MATRIX": str(matrix), "GO_MACRO_TEST_GO_BIN": str(fake_go)})
                self.assertNotEqual(0, result.returncode, result.stdout + result.stderr)
                self.assertTrue(
                    "test output is not valid successful framework evidence" in result.stderr
                    or "framework results require positive tests and zero skipped/failed" in result.stderr,
                    result.stdout + result.stderr,
                )


if __name__ == "__main__":
    unittest.main()
