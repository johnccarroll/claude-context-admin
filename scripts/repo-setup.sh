#!/usr/bin/env bash
# Locks down the public repo and the Homebrew tap: rulesets, release environments, Actions
# policy and GitHub's security features. Safe to run again; it sets the same state each time.
#
#   scripts/repo-setup.sh            # settings only
#   scripts/repo-setup.sh secrets    # then asks for each secret (docs/RELEASING.md)
#
# Needs `gh` signed in as the repo owner.
set -euo pipefail

OWNER=johnccarroll
REPO="$OWNER/claude-context-admin"
TAP="$OWNER/homebrew-tap"
ME="$(gh api user --jq .id)"
ADMIN='{"actor_id":5,"actor_type":"RepositoryRole","bypass_mode":"always"}' # repository admins

api() { gh api --silent -H "X-GitHub-Api-Version: 2022-11-28" "$@"; }

# Creates the ruleset named in $2, or replaces it if one by that name exists.
ruleset() {
  local repo="$1" name="$2" body="$3" id
  id="$(gh api "repos/$repo/rulesets" --jq ".[] | select(.name == \"$name\") | .id")"
  if [ -n "$id" ]; then
    api -X PUT "repos/$repo/rulesets/$id" --input - <<<"$body"
  else
    api -X POST "repos/$repo/rulesets" --input - <<<"$body"
  fi
  echo "ruleset $repo: $name"
}

for r in "$REPO" "$TAP"; do
  # History on main can't be rewritten or deleted, by anyone.
  ruleset "$r" "main: history" '{"name":"main: history","target":"branch","enforcement":"active",
    "conditions":{"ref_name":{"include":["~DEFAULT_BRANCH"],"exclude":[]}},
    "rules":[{"type":"deletion"},{"type":"non_fast_forward"}],"bypass_actors":[]}'
  api -X PATCH "repos/$r" -F has_wiki=false -F has_projects=false -F delete_branch_on_merge=true
  api -X PUT "repos/$r/vulnerability-alerts"
  api -X PATCH "repos/$r" --input - <<<'{"security_and_analysis":{"secret_scanning":{"status":"enabled"},
    "secret_scanning_push_protection":{"status":"enabled"}}}'
done

# Everyone but the maintainer goes through a pull request that passes CI.
ruleset "$REPO" "main: review" "{\"name\":\"main: review\",\"target\":\"branch\",\"enforcement\":\"active\",
  \"conditions\":{\"ref_name\":{\"include\":[\"~DEFAULT_BRANCH\"],\"exclude\":[]}},
  \"rules\":[{\"type\":\"pull_request\",\"parameters\":{\"required_approving_review_count\":0,
      \"dismiss_stale_reviews_on_push\":true,\"require_code_owner_review\":false,
      \"require_last_push_approval\":false,\"required_review_thread_resolution\":false}},
    {\"type\":\"required_status_checks\",\"parameters\":{\"strict_required_status_checks_policy\":false,
      \"required_status_checks\":[{\"context\":\"test\",\"integration_id\":15368}]}}],
  \"bypass_actors\":[$ADMIN]}"

# A v* tag is a release: only admins can create, move or delete one.
ruleset "$REPO" "release tags" "{\"name\":\"release tags\",\"target\":\"tag\",\"enforcement\":\"active\",
  \"conditions\":{\"ref_name\":{\"include\":[\"refs/tags/v*\"],\"exclude\":[]}},
  \"rules\":[{\"type\":\"creation\"},{\"type\":\"update\"},{\"type\":\"deletion\"}],
  \"bypass_actors\":[$ADMIN]}"

# Release environments: v* tags only. "release" (signing) waits for the maintainer's approval.
api -X PUT "repos/$REPO/environments/release" --input - <<<"{\"prevent_self_review\":false,
  \"reviewers\":[{\"type\":\"User\",\"id\":$ME}],
  \"deployment_branch_policy\":{\"protected_branches\":false,\"custom_branch_policies\":true}}"
api -X PUT "repos/$REPO/environments/publish" --input - <<<'{"deployment_branch_policy":
  {"protected_branches":false,"custom_branch_policies":true}}'
for env in release publish; do
  if ! gh api "repos/$REPO/environments/$env/deployment-branch-policies" --jq '.branch_policies[].name' | grep -qxF 'v*'; then
    api -X POST "repos/$REPO/environments/$env/deployment-branch-policies" -f name='v*' -f type=tag
  fi
  echo "environment: $env (v* tags only)"
done

# Actions: read-only token by default, no PR approvals by Actions, only GitHub's own actions plus
# the one third-party action we use, every action pinned to a full commit SHA, and workflows from
# outside contributors' forks wait for approval.
api -X PUT "repos/$REPO/actions/permissions" -F enabled=true -f allowed_actions=selected -F sha_pinning_required=true
api -X PUT "repos/$REPO/actions/permissions/selected-actions" --input - <<<'{"github_owned_allowed":true,
  "verified_allowed":false,"patterns_allowed":["oven-sh/setup-bun@*"]}'
api -X PUT "repos/$REPO/actions/permissions/workflow" -f default_workflow_permissions=read -F can_approve_pull_request_reviews=false
api -X PUT "repos/$REPO/actions/permissions/fork-pr-contributor-approval" -f approval_policy=all_external_contributors
echo "actions policy set"

# Releases can't be changed once published (assets, tag).
api -X PUT "repos/$REPO/immutable-releases"
# The tap only receives commits; it runs no workflows.
api -X PUT "repos/$TAP/actions/permissions" -F enabled=false

# Security features: private vulnerability reporting, Dependabot security updates, CodeQL.
api -X PUT "repos/$REPO/private-vulnerability-reporting"
api -X PUT "repos/$REPO/automated-security-fixes"
api -X PATCH "repos/$REPO/code-scanning/default-setup" --input - <<<'{"state":"configured",
  "languages":["actions","go","javascript-typescript"],"query_suite":"extended"}'
echo "security features on"

[ "${1:-}" = secrets ] || { echo "Done. Run again with 'secrets' to add the release secrets."; exit 0; }

# Each value is read here without echo and encrypted by gh before it leaves this Mac.
secret() { # env name [file]: from a file (base64) or a hidden prompt
  if [ -n "${3:-}" ]; then
    [ -s "$3" ] || { echo "no file at $3" >&2; exit 1; }
    base64 -i "$3" | tr -d '\n' | gh secret set "$2" --env "$1" --repo "$REPO"
  else
    local v
    read -rsp "$2: " v && echo
    [ -n "$v" ] || { echo "$2 is empty; nothing set" >&2; exit 1; }
    printf '%s' "$v" | gh secret set "$2" --env "$1" --repo "$REPO"
    unset v
  fi
}
read -rp "Path to the Developer ID .p12: " p12
p12="${p12/#\~/$HOME}"
secret release MACOS_CERT_P12 "$p12"
secret release MACOS_CERT_PASSWORD
read -rp "Path to the notary API key (.p8, Developer role): " p8
p8="${p8/#\~/$HOME}"
secret release NOTARY_KEY_P8 "$p8"
secret release NOTARY_KEY_ID
secret release NOTARY_ISSUER_ID
secret publish TAP_TOKEN
echo "Secrets set. Delete the .p12 and .p8: rm -P '$p12' '$p8'"
