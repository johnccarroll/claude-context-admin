#!/usr/bin/env python3
"""Builds a fictional home folder for screenshots and demos: Sam, a staff engineer at Northwind,
with six projects (and one renamed), ~80 linked memories, instructions, plugins, MCP servers,
skills, agents, hooks, usage history, Claude's pending suggestions, and something for every Review
check to catch. Nothing in it is real.

    scripts/demo-home.py /tmp/cca-demo && cca --home /tmp/cca-demo
"""
import json
import os
import shutil
import subprocess
import sys
import time

H = os.path.realpath(sys.argv[1])  # /var -> /private/var on macOS: write the spelling cca resolves
NOW = time.time()
DAY = 86400


def enc(p):
    return p.replace("/", "-").replace(".", "-")


def put(path, text, days_ago=None):
    os.makedirs(os.path.dirname(path), exist_ok=True)
    with open(path, "w") as f:
        f.write(text.lstrip("\n"))
    if days_ago is not None:
        t = NOW - days_ago * DAY
        os.utime(path, (t, t))


def iso(days_ago, hour=10):
    return time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime(NOW - days_ago * DAY + hour * 3600))


def repo(name):
    p = f"{H}/code/{name}"
    os.makedirs(p, exist_ok=True)
    subprocess.run(["git", "init", "-q", p], check=True)
    return p


# ---------- memories ----------
# (stem, type, title, one-line summary, body, days since last edit). Bodies link with [[name]],
# where name is the stem without its type prefix, dashes for underscores.
GLOBAL = [
    ("user_role", "user", "Role and stack", "Staff engineer at Northwind; TypeScript and Go, deploys on Fly.io",
     "Sam leads the web platform team. TypeScript on the front end, Go for services. Works with [[team-map]] daily.", 40),
    ("user_team_map", "user", "Team map", "Priya owns storefront, Marcus owns payments, Lena owns mobile",
     "Ask Priya about checkout UX, Marcus about the ledger, Lena about releases. On-call rota in [[incident-process]].", 22),
    ("feedback_small_prs", "feedback", "Small pull requests", "Keep PRs under ~300 lines; split refactors from features",
     "Split refactors from behaviour changes.\n\n**Why:** reviews stall on big diffs.\n\n**How to apply:** if a change needs \"and\" in its title, make two PRs. See [[conventional-commits]] and [[code-review]].", 12),
    ("feedback_conventional_commits", "feedback", "Conventional commits", "feat/fix/chore prefixes, imperative, 50-char subject",
     "Use Conventional Commits.\n\n**Why:** the changelog is generated from them by [[release-train]].", 30),
    ("feedback_code_review", "feedback", "Code review", "Review for bugs first, then tests, then style",
     "Leave one summary comment with the must-fix items on top. Pairs with [[small-prs]].", 14),
    ("feedback_no_mocks_db", "feedback", "Real database in tests", "Integration tests hit a real Postgres in Docker, never mocks",
     "**Why:** a mocked migration passed while prod broke in March.\n\n**How to apply:** use `make test-db`. Setup in [[postgres-local]]; migrations follow [[migrations]].", 9),
    ("reference_postgres_local", "reference", "Local Postgres", "make test-db starts Postgres 17 on :54329 with seed data",
     "Container `nw-pg`, user `dev`, database `northwind_test`. Used by [[no-mocks-db]].", 20),
    ("feedback_migrations", "feedback", "Safe migrations", "Expand, migrate, contract; never drop a column in the same deploy",
     "**Why:** rolling deploys run old and new code side by side.\n\n**How to apply:** three PRs. See [[deploy-checklist]].", 16),
    ("reference_fly_regions", "reference", "Fly.io regions", "Production runs in iad and fra; staging only in iad",
     "Scale with `fly scale count 3 --region iad,fra`. Terraform owns the rest: [[terraform-layout]].", 55),
    ("feedback_explain_tradeoffs", "feedback", "Explain trade-offs", "Offer two options with a recommendation, not five",
     "Sam wants a recommendation, not a survey.", 3),
    ("reference_incident_process", "reference", "Incident process", "Page via PagerDuty; write the timeline in the incident doc as you go",
     "Sev 1 pages the on-call and Sam. Postmortem within 5 days using [[incident-report]] notes. Dashboards: [[observability]].", 33),
    ("reference_observability", "reference", "Observability", "Logs in Grafana Loki, traces in Tempo, alerts in Grafana",
     "Every service exports OTLP. Payment alerts are in [[ledger-alerts]].", 27),
    ("feedback_deploy_checklist", "feedback", "Deploy checklist", "Migrations first, feature flag off, watch error rate for 15 minutes",
     "**Why:** two outages came from flags shipped on.\n\n**How to apply:** follow it for every prod deploy; [[release-train]] links here.", 5),
    ("project_release_train", "project", "Release train", "Web ships daily; mobile every other Tuesday; changelog from commits",
     "Release notes come from the release-notes skill. Follows [[deploy-checklist]] and [[conventional-commits]].", 7),
    ("feedback_typescript_strict", "feedback", "Strict TypeScript", "strict on, no any, exhaustive switches with never",
     "**Why:** an unhandled enum value shipped a blank checkout step.\n\n**How to apply:** `satisfies` for config objects. Reviews per [[code-review]].", 18),
    ("feedback_go_style", "feedback", "Go style", "Errors wrap with %w; context first; no global state",
     "**Why:** lost causes made incidents slow to debug ([[incident-process]]).", 24),
    ("reference_oncall", "reference", "On-call", "Weekly rota, Monday handover at 10:00; Sam is backup for payments",
     "Escalation and paging in [[incident-process]]. Dashboards in [[observability]].", 15),
    ("feedback_writing_docs", "feedback", "Writing docs", "Docs live next to code; ADRs for anything hard to reverse",
     "**Why:** the wiki drifted. ADR template in each repo's docs/adr.", 48),
    ("reference_feature_flags_policy", "reference", "Flag policy", "Flags default off in prod and are removed two sprints after 100%",
     "Shipping a flag on needs a note in the [[deploy-checklist]].", 13),
    ("reference_secrets", "reference", "Secrets", "1Password for humans, Fly secrets for services; never in .env files in git",
     "Rotate on offboarding. Incidents involving keys follow [[incident-process]].", 38),
]

PROJECTS = {
    "storefront": [
        ("project_overview", "project", "Storefront overview", "Next.js 16 shop; app router, Stripe checkout, Vercel previews",
         "The customer-facing shop. Checkout talks to [[payments-api-contract]]. Styling from [[tokens-source]] in the design system. Owner: see [[team-map]].", 2),
        ("project_checkout_flow", "project", "Checkout flow", "Cart → address → payment → confirm; payment step is a Stripe Element",
         "Errors map to RFC 9457 problems from [[payments-api-contract]]. Address autocomplete is behind [[feature-flags]].", 4),
        ("feedback_image_sizes", "feedback", "Responsive images", "Always pass sizes to next/image; LCP regressed without it",
         "**Why:** LCP went from 1.9s to 3.4s on mobile. Budget in [[web-vitals]].", 6),
        ("reference_web_vitals", "reference", "Web vitals budget", "LCP under 2.5s, INP under 200ms on a mid-range Android",
         "Checked in CI with Lighthouse. Images: [[image-sizes]].", 19),
        ("project_payments_api_contract", "project", "Payments API contract", "POST /charges is idempotent via Idempotency-Key",
         "Retries must reuse the key. Errors follow RFC 9457.", 18),
        ("project_launch_q4", "project", "Q4 launch", "Holiday catalog ships Nov 14; feature freeze Nov 7",
         "Owner: Priya. Freeze means fixes only. Checklist: [[launch-checklist]]. Flags: [[feature-flags]].", 1),
        ("reference_feature_flags", "reference", "Feature flags", "Flags live in LaunchDarkly; names are kebab-case, scoped by team",
         "Default off in prod. Clean up within two sprints of 100%. Deploy rules: [[deploy-checklist]].", 13),
        ("feedback_a11y", "feedback", "Accessibility", "Every new page passes axe with zero serious issues",
         "**Why:** the checkout audit found 14 issues in March.\n\n**How to apply:** run the a11y-check skill. Components from [[component-api]].", 10),
        ("project_search", "project", "Product search", "Typesense on Fly; synonyms file in config/search",
         "Reindex nightly from the catalog export in [[catalog-export]].", 45),
        ("project_cart_ttl", "project", "Cart expiry", "Carts expire after 7 days; reservations after 15 minutes",
         "Reservation timer starts at the payment step of [[checkout-flow]].", 9),
        ("reference_cms", "reference", "CMS", "Sanity holds marketing copy; product data comes from the catalog",
         "Preview drafts with `?preview=1`. Images follow [[image-sizes]].", 29),
        ("feedback_seo_rules", "feedback", "SEO rules", "Canonical URLs on every product; no client-only routes for catalog pages",
         "**Why:** a client-only filter page dropped out of search.\n\n**How to apply:** server components for catalog routes. Budget in [[web-vitals]].", 16),
        ("project_error_pages", "project", "Error pages", "Branded 404/500 with search; errors report to Sentry with the request id",
         "Tracing per [[observability]].", 34),
    ],
    "payments-api": [
        ("project_overview", "project", "Payments API overview", "Go service; Stripe + ledger in Postgres; gRPC internally",
         "Ledger tables are append-only: [[ledger-rules]]. Public contract: [[payments-api-contract]]. Tests use [[no-mocks-db]].", 4),
        ("feedback_ledger_rules", "feedback", "Ledger rules", "Never UPDATE ledger rows; post a reversing entry instead",
         "**Why:** auditors reconcile by replaying entries.\n\n**How to apply:** reversals reference the original id. Alerts in [[ledger-alerts]].", 25),
        ("project_payments_api_contract", "project", "Payments API contract", "POST /charges is idempotent via Idempotency-Key",
         "Retries must reuse the key. Errors follow RFC 9457.", 30),
        ("reference_ledger_alerts", "reference", "Ledger alerts", "Alert when daily ledger sum drifts from Stripe balance by > $1",
         "Grafana alert `ledger-drift`. Runbook links [[incident-process]].", 11),
        ("reference_stripe_test_cards", "reference", "Stripe test cards", "4242… succeeds; 4000 0000 0000 9995 declines",
         "Webhooks locally: `stripe listen --forward-to :8080/webhooks`.", 70),
        ("project_refunds", "project", "Refunds v2", "Partial refunds post reversing entries per line item",
         "Design follows [[ledger-rules]]; API shape extends [[payments-api-contract]]. Ships behind a flag per [[feature-flags-policy]]. Open questions in [[parity-notes]].", 8),
        ("feedback_grpc_errors", "feedback", "gRPC errors", "Map domain errors to codes in one place: internal/errs",
         "**Why:** three handlers returned Unknown for validation errors.", 21),
        ("reference_webhook_retries", "reference", "Webhook retries", "Stripe retries for 3 days; handlers must be idempotent by event id",
         "Dedup table `stripe_events`. See [[idempotency]].", 36),
        ("reference_payout_schedule", "reference", "Payout schedule", "Stripe pays out daily, 2-day rolling; finance reconciles weekly",
         "Ledger side in [[ledger-rules]].", 44),
        ("feedback_currency_rounding", "feedback", "Currency rounding", "Store minor units as int64; round half-even only at display",
         "**Why:** float cents drifted by $3.12 over a month.\n\n**How to apply:** money type in internal/money. Ties to [[ledger-rules]].", 19),
        ("project_fraud_rules", "project", "Fraud rules", "Radar rules block high-risk cards; manual review queue over $500",
         "Reviewed monthly with finance. Alerts in [[ledger-alerts]].", 52),
    ],
    "mobile": [
        ("project_overview", "project", "Mobile app overview", "React Native + Expo; shares API types with storefront",
         "OTA updates through EAS. Releases: [[mobile-release]]. Components from [[component-api]].", 8),
        ("feedback_test_on_device", "feedback", "Test on a real device", "Simulator misses haptics and push; check on an iPhone",
         "**Why:** a push permission bug only showed on device.", 11),
        ("project_mobile_release", "project", "Mobile release", "Every other Tuesday; OTA for JS-only fixes, store build otherwise",
         "Part of the [[release-train]]. Lena signs off.", 6),
        ("reference_push_setup", "reference", "Push notifications", "Expo push tokens stored per device; APNs key in EAS secrets",
         "Test on device: [[test-on-device]].", 50),
        ("project_offline_cart", "project", "Offline cart", "Cart persists in MMKV and syncs on reconnect",
         "Conflicts resolved server-side by the [[checkout-flow]] rules.", 15),
        ("reference_deep_links", "reference", "Deep links", "Universal links for /p/* and /orders/*; Android App Links verified",
         "Order links open the [[offline-cart]] screen if offline.", 26),
        ("feedback_crash_reporting", "feedback", "Crash reporting", "Sentry with source maps uploaded by EAS; release = build number",
         "**Why:** unsymbolicated crashes were useless during the March release ([[mobile-release]]).", 21),
        ("reference_app_review", "reference", "App Store review", "Review takes ~24h; never submit on Fridays",
         "Plan around the [[mobile-release]] train.", 64),
    ],
    "design-system": [
        ("project_overview", "project", "Design system overview", "React components + tokens, published as @northwind/ui",
         "Tokens: [[tokens-source]]. API conventions: [[component-api]]. Used by storefront and mobile.", 3),
        ("reference_tokens_source", "reference", "Tokens source", "tokens.json is the source; CSS and RN themes are generated",
         "Purple accent `--brand-500`. Dark mode mirrors light. Never hard-code hex in apps.", 9),
        ("feedback_component_api", "feedback", "Component API", "Props are typed unions, no boolean soup; forwardRef everything",
         "**Why:** `primary secondary large` props collided.\n\n**How to apply:** `variant` and `size` unions. Accessibility per [[a11y]].", 12),
        ("project_versioning", "project", "Versioning", "Changesets; breaking changes need a codemod",
         "Release notes generated like the [[release-train]].", 28),
        ("reference_icons", "reference", "Icons", "Lucide set, 1.5px stroke; custom icons go through design review",
         "Exported as React components alongside the [[tokens-source]].", 35),
        ("feedback_motion", "feedback", "Motion", "200ms ease-out for UI, respect prefers-reduced-motion",
         "**Why:** a carousel triggered motion sickness complaints.\n\n**How to apply:** use the `motion` tokens from [[tokens-source]].", 22),
        ("project_theming", "project", "Theming", "Brand themes switch tokens, never components",
         "Dark mode and partner themes are token sets. Components follow [[component-api]].", 30),
    ],
    "data-pipeline": [
        ("project_overview", "project", "Data pipeline overview", "dbt on Snowflake; Airflow schedules; nightly catalog export",
         "Feeds [[catalog-export]] and finance reports from the ledger ([[ledger-rules]]).", 17),
        ("project_catalog_export", "project", "Catalog export", "Nightly at 02:00 UTC; JSONL to S3, then search reindex",
         "Consumers: storefront [[search]] and mobile. Failure alerts via [[observability]].", 20),
        ("feedback_dbt_tests", "feedback", "dbt tests", "Every model has unique + not_null on its key",
         "**Why:** a duplicate order id doubled revenue in a board deck.", 31),
        ("reference_snowflake_roles", "reference", "Snowflake roles", "TRANSFORMER writes, REPORTER reads; no personal grants",
         "Requested through the infra repo: [[terraform-layout]].", 95),
        ("project_attribution", "project", "Attribution model", "Last non-direct click, 30-day window",
         "Built on the [[catalog-export]] joins plus web events. Tested per [[dbt-tests]].", 40),
        ("feedback_pii", "feedback", "PII handling", "Hash emails at ingest; raw PII only in the restricted schema",
         "**Why:** a dashboard exposed customer emails in May.\n\n**How to apply:** roles per [[snowflake-roles]].", 14),
        ("reference_backfills", "reference", "Backfills", "Backfill one partition at a time with --full-refresh off",
         "Watch warehouse credits; see [[dbt-tests]] before and after.", 58),
    ],
    "infra": [
        ("project_overview", "project", "Infra overview", "Terraform for Fly, Cloudflare, Snowflake grants; Atlantis applies",
         "Layout: [[terraform-layout]]. Regions: [[fly-regions]].", 23),
        ("reference_terraform_layout", "reference", "Terraform layout", "One root module per environment; shared modules in modules/",
         "Plan in PR via Atlantis; apply after approval. Never apply from a laptop.", 26),
        ("feedback_no_laptop_apply", "feedback", "No laptop applies", "Terraform applies only through Atlantis",
         "**Why:** a laptop apply used a stale state lock in May.", 60),
        ("reference_idempotency", "reference", "Idempotency keys", "Clients send UUIDv7 keys; servers store them for 24 hours",
         "Used by [[payments-api-contract]] and [[webhook-retries]].", 41),
        ("reference_cloudflare_waf", "reference", "Cloudflare WAF", "Managed rules on; custom rule blocks /admin outside the VPN",
         "Managed in Terraform per [[terraform-layout]].", 47),
        ("feedback_cost_tags", "feedback", "Cost tags", "Every resource tags team and service; untagged resources fail the plan",
         "**Why:** a $4k/month cluster had no owner.\n\n**How to apply:** the tags module in [[terraform-layout]].", 33),
        ("reference_dns", "reference", "DNS", "northwind.example on Cloudflare; TTL 300 for anything that fails over",
         "Records live next to the [[cloudflare-waf]] config.", 80),
    ],
}

# A folder that was renamed: its memories load nowhere until moved.
MOVED = ("marketing-site", "website", [
    ("project_overview", "project", "Website overview", "Astro marketing site at northwind.example; deploys from main",
     "Blog posts in content/. The website repo replaced the old marketing-site name.", 120),
    ("feedback_copy_tone", "feedback", "Copy tone", "Plain, friendly, no exclamation marks",
     "Marketing reviews every headline.", 140),
])

# Extras that give Review something to catch.
OLD_FORMAT = {"mobile": "reference_push_setup"}               # legacy top-level type:
BAD_YAML = {"infra": "feedback_no_laptop_apply"}               # a quoted phrase with more after it
NEVER_OPENED = {"payments-api": ["reference_stripe_test_cards"], "data-pipeline": ["reference_snowflake_roles"],
                "storefront": ["project_search"]}              # old, and no session ever read them


import re
GLOBAL_NAMES = {g[0].split("_", 1)[1].replace("_", "-") for g in GLOBAL}
BROKEN_ON_PURPOSE = {"launch-checklist", "parity-notes"}


def reachable(body, items):
    """Claude reads links within a project's memories and Everywhere's; anything else is plain text
    here, except the broken links Review is meant to find."""
    names = {i[0].split("_", 1)[1].replace("_", "-") for i in items} | GLOBAL_NAMES
    return re.sub(r"\[\[([^\]]+)\]\]", lambda m: m.group(0) if m.group(1) in names | BROKEN_ON_PURPOSE else m.group(1).replace("-", " "), body)


SHARED = ["deploy-checklist", "code-review", "observability", "small-prs", "incident-process", "feature-flags-policy",
          "typescript-strict", "go-style", "migrations", "release-train", "secrets", "oncall"]
SEE = ["Related: [[{}]].", "Team norm: [[{}]].", "See also [[{}]].", "Follows [[{}]]."]


def memories(items, owner_path, folder=None):
    d = f"{H}/.claude/projects/{enc(owner_path)}/memory"
    if folder in PROJECTS:  # cite shared notes so projects connect through Everywhere
        k = list(PROJECTS).index(folder)
        items = [(st, ty, ti, su, bo + " " + SEE[(i + k) % 4].format(SHARED[(i * 5 + k * 3) % len(SHARED)]), da)
                 for i, (st, ty, ti, su, bo, da) in enumerate(items)]
    items = [(st, ty, ti, su, reachable(bo, items), da) for st, ty, ti, su, bo, da in items]
    lines = ["# Memory Index", ""]
    for stem, typ, title, summary, body, days in items:
        name = stem.split("_", 1)[1].replace("_", "-")
        if folder and OLD_FORMAT.get(folder) == stem:
            head = f"---\nname: {name}\ndescription: {json.dumps(summary)}\ntype: {typ}\n---\n"
        elif folder and BAD_YAML.get(folder) == stem:
            head = f"---\nname: {name}\ndescription: \"Terraform applies\" only through Atlantis\nmetadata:\n  type: {typ}\n---\n"
        else:
            head = f"---\nname: {name}\ndescription: {json.dumps(summary)}\nmetadata:\n  type: {typ}\n---\n"
        put(f"{d}/{stem}.md", f"{head}\n{body}\n", days)
        lines.append(f"- [{title}]({stem}.md) — {summary}")
    put(f"{d}/MEMORY.md", "\n".join(lines) + "\n")
    return d


# It deletes and rebuilds H, so refuse anything that could be real: the home folder or a folder
# above it, or a non-empty folder this script didn't make.
home = os.path.realpath(os.path.expanduser("~"))
if H == "/" or home == H or home.startswith(H.rstrip("/") + "/"):
    sys.exit(f"refusing: {H} is your home folder or contains it")
if os.path.exists(H) and os.listdir(H) and not os.path.exists(f"{H}/.cca-demo"):
    sys.exit(f"refusing: {H} isn't empty and wasn't made by this script")
if os.path.exists(H):
    shutil.rmtree(H)
os.makedirs(f"{H}/.claude/projects")
open(f"{H}/.cca-demo", "w").close()

gdir = memories(GLOBAL, H)
mem_dirs = {}
for name, items in PROJECTS.items():
    p = repo(name)
    mem_dirs[name] = (p, memories(items, p, name))

old, new, items = MOVED
repo(new)
d = memories(items, f"{H}/code/{old}")
put(f"{os.path.dirname(d)}/sessions-index.json", json.dumps({"version": 1, "entries": [], "originalPath": f"{H}/code/{old}"}))

# ---------- usage: sessions that read memories and call tools ----------
put(f"{H}/.claude/projects/{enc(H)}/s0.jsonl", "\n".join(json.dumps(e, separators=(",", ":")) for e in [{"cwd": H, "type": "user", "timestamp": iso(2)}] + [
    {"cwd": H, "type": "assistant", "timestamp": iso(1 + i % 20), "message": {"content": [{"type": "tool_use", "name": "Read", "input": {"file_path": f"{gdir}/{g[0]}.md"}}]}}
    for i, g in enumerate(GLOBAL) for _ in range(1 + i % 3)]) + "\n")
TOOLS = {"storefront": ["mcp__github__create_pull_request", "mcp__playwright__browser_navigate", "mcp__sentry__get_issue"],
         "payments-api": ["mcp__github__create_pull_request", "mcp__postgres__query", "mcp__stripe__list_charges"],
         "mobile": ["mcp__github__create_pull_request", "mcp__linear__get_issue"],
         "design-system": ["mcp__figma__get_file", "mcp__github__create_pull_request"],
         "data-pipeline": ["mcp__snowflake__run_query"], "infra": ["mcp__github__create_pull_request"]}
SKILLS = {"storefront": ["release-notes", "a11y-check", "frontend-design", "pr-checklist"], "payments-api": ["release-notes", "sql-review", "debug-payment", "pr-checklist"],
          "mobile": ["release-notes", "pr-checklist"], "design-system": ["frontend-design", "pr-checklist"], "data-pipeline": ["sql-review"], "infra": ["pr-checklist"]}
for n, (name, items) in enumerate(PROJECTS.items()):
    p, md = mem_dirs[name]
    skip = set(NEVER_OPENED.get(name, []))
    events = [{"cwd": p, "type": "user", "timestamp": iso(1 + n)}]
    reads = [(md, s[0]) for s in items if s[0] not in skip] + [(gdir, g[0]) for g in GLOBAL[(n * 2) % 10:(n * 2) % 10 + 5]]
    for i, (folder, stem) in enumerate(reads):
        for k in range(1 + (i * 7 + n) % 4):  # uneven counts, so the busiest memories stand out
            day = 1 + (i + k + n) % 26
            uses = [{"type": "tool_use", "name": "Read", "input": {"file_path": f"{folder}/{stem}.md"}}]
            if k == 0 and i < len(TOOLS[name]):
                uses.append({"type": "tool_use", "name": TOOLS[name][i], "input": {}})
            if k == 0 and i < len(SKILLS[name]):
                uses.append({"type": "tool_use", "name": "Skill", "input": {"skill": SKILLS[name][i]}})
            events.append({"cwd": p, "type": "assistant", "timestamp": iso(day, 9 + k), "message": {"content": uses}})  # every line carries cwd, as Claude Code writes them
    put(f"{H}/.claude/projects/{enc(p)}/s1.jsonl", "\n".join(json.dumps(e, separators=(",", ":")) for e in events) + "\n")

# ---------- instructions ----------
put(f"{H}/.claude/CLAUDE.md", """
# Sam's defaults

- Explain trade-offs briefly, then recommend one option.
- Run the tests before saying something works.
- Prefer small, reviewable changes.
@~/.claude/rules/style.md
@~/.claude/rules/security.md
""")
put(f"{H}/.claude/rules/style.md", "# Style\n\n- 2-space indent in TypeScript, gofmt in Go.\n- Name things for what they do, not how.\n")
put(f"{H}/.claude/rules/security.md", "# Security\n\n- Never paste secrets into code or memory; use the 1Password CLI.\n- Treat webhook payloads as untrusted input.\n")
put(f"{H}/code/storefront/AGENTS.md", """
# Storefront

You MUST use the design tokens.
NEVER hard-code colors.
ALWAYS run `pnpm test` before committing.
IMPORTANT: Do NOT edit generated files in src/gen.
CRITICAL: NEVER skip the visual tests.
Think step by step before large refactors.

Run `pnpm dev` and open http://localhost:3000.
@docs/architecture.md
""")
put(f"{H}/code/storefront/docs/architecture.md", "# Architecture\n\nApp router; server components fetch from the payments API and the catalog.\n")
put(f"{H}/code/payments-api/CLAUDE.md", "# Payments API\n\nGo 1.26. `make test` runs unit and integration tests against Postgres.\n@docs/runbook.md\n")
put(f"{H}/code/mobile/AGENTS.md", "# Mobile\n\nExpo SDK 55. `pnpm ios` runs the simulator; release notes come from the release-notes skill.\n")
put(f"{H}/code/design-system/CLAUDE.md", "# Design system\n\nStorybook on :6006. Every component ships a story and a test.\nUse claude-3-opus for long design reviews.\n")
put(f"{H}/code/data-pipeline/CLAUDE.md", "# Data pipeline\n\n`dbt build --select state:modified+` before opening a PR.\n")
put(f"{H}/code/infra/CLAUDE.md", "# Infra\n\nNever run `terraform apply` locally. Plans come from Atlantis.\n")
put(f"{H}/code/infra/.claude/rules/terraform.md", "---\npaths: [\"**/*.tf\"]\n---\n# Terraform\n\nPin provider versions. One resource per file for anything over 50 lines.\n")

# ---------- skills, commands, agents ----------
put(f"{H}/.claude/skills/release-notes/SKILL.md", """
---
name: release-notes
description: Draft release notes from merged PRs since the last tag
---
Collect merged PRs since the last tag, group by type, write user-facing notes.
""")
put(f"{H}/.claude/skills/incident-report/SKILL.md", """
---
name: incident-report
description: Turn an incident channel and timeline into a blameless postmortem
---
Gather the timeline, impact, root cause and follow-ups. Keep it blameless.
""")
# an oversized skill: its whole text loads every time it runs
put(f"{H}/.claude/skills/sql-review/SKILL.md", "---\nname: sql-review\ndescription: Review SQL and dbt models for correctness and cost\n---\n"
    + "\n".join(f"{i}. Check rule {i}: joins on keys, filters before aggregates, no SELECT * in models, explain the plan for anything over a million rows."
                for i in range(1, 520)) + "\n")
put(f"{H}/.claude/agents/reviewer.md", "---\nname: reviewer\ndescription: Reviews a diff for bugs and missing tests\n---\nReview the diff. Report bugs first.\n")
put(f"{H}/.claude/agents/migration-planner.md", "---\nname: migration-planner\ndescription: Plans expand/contract database migrations\n---\nPlan three PRs: expand, migrate, contract.\n")
put(f"{H}/code/storefront/.claude/agents/perf-auditor.md", "---\nname: perf-auditor\ndescription: Finds the biggest web-vitals regressions on a page\n---\nProfile the page, rank by LCP and INP impact.\n")
for r in ("storefront", "mobile", "design-system"):  # the same command copied into three repos
    put(f"{H}/code/{r}/.claude/commands/preview.md", "---\ndescription: Open the preview deployment for this branch\n---\nFind the preview URL and open it.\n")
for r in ("storefront", "design-system"):
    put(f"{H}/code/{r}/.claude/skills/a11y-check/SKILL.md", "---\nname: a11y-check\ndescription: Audit a page for accessibility issues\n---\nRun axe on the page and fix what it finds.\n")
put(f"{H}/code/payments-api/.claude/commands/replay-webhook.md", "---\ndescription: Replay a Stripe webhook by event id\n---\nFetch the event with the Stripe CLI and POST it to the local handler.\n")
put(f"{H}/code/infra/.claude/commands/plan.md", "---\ndescription: Comment an Atlantis plan on this PR\n---\nRun `atlantis plan` for the changed roots.\n")

# ---------- plugins (installed the way Claude Code installs them) ----------
MARKET = "northwind-tools"
PLUGINS = [
    ("pr-review-kit", "Review pull requests with checklists and a reviewer agent", True,
     {"skills/pr-checklist/SKILL.md": "---\nname: pr-checklist\ndescription: Walk a PR through the team checklist\n---\nCheck tests, migrations, flags and docs.\n",
      "agents/security-reviewer.md": "---\nname: security-reviewer\ndescription: Looks for injection, authz and secret handling issues\n---\nReview for security issues.\n",
      "commands/review.md": "---\ndescription: Review the current branch\n---\nRun the checklist on the diff.\n"}),
    ("stripe-helpers", "Stripe MCP server and payment debugging skills", True,
     {".mcp.json": json.dumps({"mcpServers": {"stripe": {"command": "npx", "args": ["-y", "@stripe/mcp", "--tools=all"], "env": {"STRIPE_SECRET_KEY": "${STRIPE_SECRET_KEY}"}}}}),
      "skills/debug-payment/SKILL.md": "---\nname: debug-payment\ndescription: Trace a failed payment from Stripe event to ledger entry\n---\nFind the event, the charge and the ledger rows.\n"}),
    ("frontend-design", "Design-quality skills for web UI", True,
     {"skills/frontend-design/SKILL.md": "---\nname: frontend-design\ndescription: Build distinctive, accessible web interfaces\n---\nPick a clear visual direction; check contrast and focus states.\n"}),
    ("security-guidance", "Warns before risky edits", True,
     {"hooks/hooks.json": json.dumps({"hooks": {"PreToolUse": [{"matcher": "Edit|Write", "hooks": [{"type": "command", "command": "${CLAUDE_PLUGIN_ROOT}/hooks/check.sh"}]}]}}),
      "hooks/check.sh": "#!/bin/sh\nexit 0\n"}),
    ("docs-lookup", "Up-to-date library docs over MCP", False,
     {".mcp.json": json.dumps({"mcpServers": {"docs": {"type": "http", "url": "https://docs-mcp.example.com/mcp"}}}),
      "skills/find-docs/SKILL.md": "---\nname: find-docs\ndescription: Look up current docs for a library before using it\n---\nSearch the docs server, quote the relevant section.\n"}),
]
mdir = f"{H}/.claude/plugins/marketplaces/{MARKET}"
installed, enabled = {}, {}
for name, desc, on, files in PLUGINS:
    ip = f"{H}/.claude/plugins/cache/{MARKET}/{name}/1.2.0"
    put(f"{ip}/.claude-plugin/plugin.json", json.dumps({"name": name, "version": "1.2.0", "description": desc, "author": {"name": "Northwind"}}, indent=2))
    for rel, text in files.items():
        put(f"{ip}/{rel}", text)
    put(f"{mdir}/plugins/{name}/.claude-plugin/plugin.json", json.dumps({"name": name, "version": "1.2.0", "description": desc}, indent=2))
    installed[f"{name}@{MARKET}"] = [{"scope": "user", "installPath": ip, "version": "1.2.0", "installedAt": iso(60),
                                      "lastUpdated": iso(9), "gitCommitSha": "0" * 40}]
    enabled[f"{name}@{MARKET}"] = on
put(f"{mdir}/.claude-plugin/marketplace.json", json.dumps({"name": MARKET, "owner": {"name": "Northwind"},
    "plugins": [{"name": n, "source": f"./plugins/{n}", "description": d} for n, d, _, _ in PLUGINS]}, indent=2))
put(f"{H}/.claude/plugins/installed_plugins.json", json.dumps({"version": 2, "plugins": installed}, indent=2))
put(f"{H}/.claude/plugins/known_marketplaces.json", json.dumps({MARKET: {
    "source": {"source": "github", "repo": "northwind/claude-plugins"}, "installLocation": mdir, "lastUpdated": iso(9)}}, indent=2))

# ---------- settings and hooks ----------
put(f"{H}/.claude/settings.json", json.dumps({"enabledPlugins": enabled, "hooks": {
    "SessionStart": [{"hooks": [{"type": "command", "command": "git fetch --quiet --all"}]}],
    "PostToolUse": [{"matcher": "Write|Edit", "hooks": [{"type": "command", "command": "npx prettier --write \"$CLAUDE_FILE_PATHS\""}]}],
    "Stop": [{"hooks": [{"type": "command", "command": "~/.claude/hooks/notify.sh"}]}],  # the script is gone
}}, indent=2))
put(f"{H}/code/payments-api/.claude/settings.json", json.dumps({"hooks": {
    "PreToolUse": [{"matcher": "Bash", "hooks": [{"type": "command", "command": "./scripts/guard-prod.sh"}]}]}}, indent=2))
put(f"{H}/code/payments-api/scripts/guard-prod.sh", "#!/bin/sh\n# refuses commands that touch prod\nexit 0\n")
put(f"{H}/code/infra/.claude/settings.json", json.dumps({"hooks": {
    "PreToolUse": [{"matcher": "Bash", "hooks": [{"type": "command", "command": "./scripts/block-apply.sh"}]}]}}, indent=2))
put(f"{H}/code/infra/scripts/block-apply.sh", "#!/bin/sh\n# blocks terraform apply\nexit 0\n")

# ---------- MCP servers: key names only; the values are placeholders ----------
put(f"{H}/.claude.json", json.dumps({"mcpServers": {
    "github": {"command": "npx", "args": ["-y", "@modelcontextprotocol/server-github"], "env": {"GITHUB_TOKEN": "${GITHUB_TOKEN}"}},
    "linear": {"type": "http", "url": "https://mcp.linear.app/mcp"},
    "sentry": {"type": "http", "url": "https://mcp.sentry.dev/mcp"},
    "figma": {"type": "http", "url": "https://mcp.figma.com/mcp"},
    "notion": {"type": "http", "url": "https://mcp.notion.com/mcp"},  # never used: Review suggests turning it off
}, "projects": {
    f"{H}/code/payments-api": {"mcpServers": {
        "postgres": {"command": "npx", "args": ["-y", "@modelcontextprotocol/server-postgres", "postgresql://localhost:54329/northwind_test"]}}},
    f"{H}/code/data-pipeline": {"mcpServers": {
        "snowflake": {"command": "uvx", "args": ["snowflake-mcp"], "env": {"SNOWFLAKE_PASSWORD": "${SNOWFLAKE_PASSWORD}"}}}},
}}, indent=2))
put(f"{H}/code/storefront/.mcp.json", json.dumps({"mcpServers": {
    "playwright": {"command": "npx", "args": ["@playwright/mcp@latest"]}}}, indent=2))

# ---------- Claude's suggestions waiting in Review, and some history ----------
data = f"{H}/.claude-context-admin"
sf = mem_dirs["storefront"][1]
proposals = [
    {"id": "demo0001", "at": iso(0.2), "op": "memory-save", "status": "pending",
     "reason": "The web-vitals note still says LCP under 2.5s, but the team agreed on 2.0s in this week's review.",
     "args": {"path": f"{sf}/reference_web_vitals.md", "description": "LCP under 2.0s, INP under 200ms on a mid-range Android",
              "body": "Checked in CI with Lighthouse. Images: [[image-sizes]]. Budget tightened to 2.0s after the Q3 review."}},
    {"id": "demo0002", "at": iso(0.4), "op": "memory-promote", "status": "pending",
     "reason": "Feature flags are used the same way in payments and mobile; this belongs Everywhere.",
     "args": {"path": f"{sf}/reference_feature_flags.md"}},
]
put(f"{data}/proposals.jsonl", "".join(json.dumps(p) + "\n" for p in proposals))
activity = [
    {"id": "demo-a1", "at": iso(6, 9), "who": "you", "title": "Turned off docs-lookup", "detail": "Plugin · unused for 90 days"},
    {"id": "demo-a2", "at": iso(5, 14), "who": "claude", "title": "Claude wrote Refunds v2", "detail": "New project note in Payments Api"},
    {"id": "demo-a3", "at": iso(4, 10), "who": "you", "title": "Merged Payments API contract into Everywhere"},
    {"id": "demo-a4", "at": iso(3, 8), "who": "claude", "title": "Claude edited Checkout flow", "detail": "Added the address-autocomplete flag."},
    {"id": "demo-a5", "at": iso(3, 16), "who": "you", "title": "Accepted Claude’s suggestion: fix a broken link", "detail": "Offline cart → Checkout flow"},
    {"id": "demo-a6", "at": iso(2, 11), "who": "you", "title": "Linked Ledger rules from Refunds v2"},
    {"id": "demo-a7", "at": iso(1, 15), "who": "you", "title": "Moved Idempotency keys to Infra"},
    {"id": "demo-a8", "at": iso(0.3, 9), "who": "claude", "title": "Claude edited Web vitals budget", "detail": "Tightened LCP to 2.0s"},
]
put(f"{data}/activity.jsonl", "".join(json.dumps(a) + "\n" for a in activity))

print(H)
