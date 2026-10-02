---
name: memory-review
description: Review Claude Code's memories, instructions and toolkit for problems and suggest fixes the user approves in Claude Context Admin. Use when the user asks to review, clean up, prune or audit memory, CLAUDE.md / AGENTS.md, skills, plugins, hooks or context usage.
---

Use the `cca` MCP tools to review, then suggest fixes; never edit memory or config files directly during a review.

1. Get the picture: `health_findings` (optionally for the current project), `context_budget` for the current project, and `toolkit_list` when plugins or MCP servers are involved.
2. Read before judging: open each memory or file you plan to change with `memory_read`.
3. For each change worth making, call `propose_change` with the op, its args (paths exactly as the tools returned them) and a reason the user can decide on in a sentence or two. Group related memories into one `bulk` proposal.
4. Tell the user what you suggested and that they can accept or dismiss each one in Claude Context Admin under Review. Everything they accept can be undone from Activity.

What to look for: broken `[[links]]`, duplicates across projects (suggest merging into Everywhere), project notes that are no longer true, memories never opened in months, an index close to its ~200-line limit, all-caps emphasis, prompting written for older models, plugins that cost context and go unused.

Keep the user's own context: reasons, decisions and facts only they know are never cruft. When unsure, leave it out.
