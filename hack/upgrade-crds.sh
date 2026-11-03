
#!/usr/bin/env bash
set -euo pipefail
ENV="${1:?usage: upgrade-crds.sh <env>}"
GEN=$(mktemp -d)
trap 'rm -rf "$GEN"' EXIT

helm template zeus-crds charts/zeus-crds --include-crds > "$GEN/all.yaml"

for crd in $(yq eval 'select(.kind == "CustomResourceDefinition") | .metadata.name' "$GEN/all.yaml"); do
  echo "Checking $crd"
  kubectl get crd "$crd" -o json 2>/dev/null \
    | jq -S '[.spec.versions[] | {name, served, storage, schema}]' > "$GEN/installed-$crd.json" || true
  yq eval "select(.kind == \"CustomResourceDefinition\" and .metadata.name == \"$crd\") | [.spec.versions[] | {name, served, storage, schema}]" \
    "$GEN/all.yaml" | jq -S . > "$GEN/new-$crd.json"

  if [[ -s "$GEN/installed-$crd.json" ]] && ! diff -q "$GEN/installed-$crd.json" "$GEN/new-$crd.json" >/dev/null; then
    jq -r '.[].schema.openAPIV3Schema | paths(scalars) as $p | $p | join(".")' "$GEN/installed-$crd.json" | sort -u > "$GEN/old-paths"
    jq -r '.[].schema.openAPIV3Schema | paths(scalars) as $p | $p | join(".")' "$GEN/new-$crd.json"       | sort -u > "$GEN/new-paths"
    if comm -23 "$GEN/old-paths" "$GEN/new-paths" | grep -q .; then
      echo "ERROR: $crd removes fields. Add a conversion webhook." >&2
      comm -23 "$GEN/old-paths" "$GEN/new-paths" >&2
      exit 1
    fi
  fi
done

kubectl apply -f "$GEN/all.yaml"
echo "CRDs applied."
