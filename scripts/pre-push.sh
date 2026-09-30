#!/usr/bin/env bash
# A git pre-push hook: refuses a push unless every commit it would send has a
# green stamp from scripts/gate-commit.sh, which ran the whole gate on that
# commit's own archive. CI runs the same gate after the push; this makes a red
# run there a surprise about the platform, never about a commit nobody gated.
#
#   ln -s ../../scripts/pre-push.sh .git/hooks/pre-push
set -euo pipefail

stamps="$(git rev-parse --git-common-dir)/gate-green"
zero="0000000000000000000000000000000000000000"
missing=0
checked=0

# git hands the hook one line per ref: <local ref> <local sha> <remote ref> <remote sha>.
while read -r local_ref local_sha _ remote_sha; do
  [[ -n "${local_ref}" ]] || continue
  [[ "${local_sha}" != "${zero}" ]] || continue # a deletion sends no commit
  if [[ "${remote_sha}" == "${zero}" ]]; then
    range=("${local_sha}" --not --remotes)
  else
    range=("${remote_sha}..${local_sha}")
  fi
  while read -r sha; do
    [[ -n "${sha}" ]] || continue
    checked=$((checked + 1))
    if [[ ! -f "${stamps}/${sha}" ]]; then
      printf 'pre-push: %s has no green gate; run scripts/gate-commit.sh %s\n' "${sha}" "${sha}" >&2
      missing=$((missing + 1))
    fi
  done < <(git rev-list "${range[@]}")
done

if [[ ${missing} -ne 0 ]]; then
  printf 'pre-push: %d of %d commit(s) ungated; nothing pushed\n' "${missing}" "${checked}" >&2
  exit 1
fi
printf 'pre-push: %d commit(s), each gated green on its own archive\n' "${checked}" >&2
