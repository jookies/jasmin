#!/usr/bin/env bash
# Run with: sudo /home/desktop/projects/jasmin/cleanup-and-install.sh
set -euo pipefail

if [ "$EUID" -ne 0 ]; then
  echo "ERROR: run as root (sudo)." >&2
  exit 1
fi

PROJECT=/home/desktop/projects/jasmin
VENV=$PROJECT/.venv
OWNER_USER=desktop
OWNER_GROUP=desktop

cd "$PROJECT"

echo "== 1) Remove root-owned build artifacts from project =="
rm -rf "$PROJECT/UNKNOWN.egg-info" \
       "$PROJECT/dist/UNKNOWN-"*.egg \
       "$PROJECT/build" \
       "$PROJECT/jasmin.egg-info"

echo "== 2) Remove stale jasmin from system Python (eggs + bin scripts + pth entry) =="
rm -rf /usr/local/lib/python3.10/dist-packages/jasmin-*.egg \
       /usr/local/lib/python3.10/dist-packages/UNKNOWN-*.egg \
       /usr/local/lib/python3.10/dist-packages/jasmin-*.dist-info
rm -f  /usr/local/bin/jasmind.py \
       /usr/local/bin/interceptord.py \
       /usr/local/bin/dlrd.py \
       /usr/local/bin/dlrlookupd.py \
       /usr/local/bin/deliversmd.py

PTH=/usr/local/lib/python3.10/dist-packages/easy-install.pth
if [ -f "$PTH" ]; then
  sed -i '/jasmin-.*-py3\.10\.egg/d; /UNKNOWN-.*-py3\.10\.egg/d' "$PTH"
fi

echo "== 3) Rebuild wheel as project owner =="
sudo -u "$OWNER_USER" "$VENV/bin/python" -m build --wheel --sdist

WHEEL=$(ls -t "$PROJECT/dist"/jasmin-*.whl | head -n1)
echo "Built: $WHEEL"

echo "== 4) Install wheel into system pip =="
pip3 install --upgrade --force-reinstall "$WHEEL"

echo "== 5) Smoke test =="
which jasmind.py
jasmind.py --help 2>&1 | head -5 || true

echo "== Done =="
