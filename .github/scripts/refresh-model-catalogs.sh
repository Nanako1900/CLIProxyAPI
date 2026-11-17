#!/usr/bin/env bash
set -euo pipefail

models_repository="${MODELS_REPOSITORY_URL:-https://github.com/router-for-me/models.git}"
models_ref="${MODELS_REPOSITORY_REF:-main}"
catalog_dir="${MODEL_CATALOG_DIR:-internal/registry/models}"
codex_catalog="$catalog_dir/codex_client_models.json"
refresh_dir="$(mktemp -d)"
codex_candidate="$refresh_dir/codex_client_models.json"
trap 'rm -rf "$refresh_dir"' EXIT

# Preserve newly shipped models before the remote catalogs catch up.
go build -o "$refresh_dir/merge-model-catalogs" ./cmd/merge_model_catalogs

git fetch --depth 1 "$models_repository" "$models_ref"
git show FETCH_HEAD:models.json > "$refresh_dir/models.json"
"$refresh_dir/merge-model-catalogs" --file "$refresh_dir/models.json" \
  --fallback "$catalog_dir/models.json" --output "$catalog_dir/models.json"

if git show FETCH_HEAD:codex_client_models.json > "$codex_candidate" &&
  go run ./cmd/validate_codex_models --file "$codex_candidate"; then
	"$refresh_dir/merge-model-catalogs" --file "$codex_candidate" \
	  --fallback "$codex_catalog" --output "$codex_catalog"
	printf 'Refreshed validated Codex client model catalog.\n'
else
	printf '::warning::Remote Codex client model catalog is missing or invalid; using embedded fallback.\n'
fi

go run ./cmd/validate_codex_models --file "$codex_catalog"
