---
name: remember
description: Save something to Claude Code's memory without creating duplicates. Use when the user says remember this, save this, or note this for next time.
---

1. Search first: call the `cca` `memory_search` tool with the key words. If a memory already covers it, update that one instead of adding another.
2. Write it the way Claude Code's memory expects: one file per fact in the memory folder, with `name`, a one-line `description` and its type (`feedback` for how to work with the user, `project` for ongoing work, `reference` for where things are, `user` for facts about the user), then the fact. For feedback and project memories add **Why:** and **How to apply:** lines.
3. Add a one-line entry for it to `MEMORY.md` in the same folder: `- [Title](file.md) — hook`.
