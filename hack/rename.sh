
#!/usr/bin/env bash
set -euo pipefail
OLD_NAME="zeus"
NEW_NAME="${1:?usage: rename.sh <new-name>}"

grep -rl --exclude-dir=.git --exclude-dir=bin \
  -e "zeus-broadcast" -e "zeus\.broadcast" -e "zeus-kernel" -e "zeus-nap-validate" -e "zeus-crds" . \
  | xargs sed -i \
    -e "s|github.com/zeus-broadcast/${OLD_NAME}-kernel|github.com/${NEW_NAME}-broadcast/${NEW_NAME}-kernel|g" \
    -e "s|${OLD_NAME}\.broadcast|${NEW_NAME}.broadcast|g" \
    -e "s|${OLD_NAME}-kernel|${NEW_NAME}-kernel|g" \
    -e "s|${OLD_NAME}-crds|${NEW_NAME}-crds|g" \
    -e "s|${OLD_NAME}-nap-validate|${NEW_NAME}-nap-validate|g" \
    -e "s|ghcr.io/zeus-broadcast|ghcr.io/${NEW_NAME}-broadcast|g"

echo "Renamed ${OLD_NAME} -> ${NEW_NAME}. Run 'make generate manifests'."
