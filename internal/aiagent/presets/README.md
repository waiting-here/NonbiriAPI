# Built-in assistant presets

These files are embedded in the binary. They contain no instance configuration, accounts, hosts or credentials.

- `auto-review.json` and `chat.json` contain the initial message arrays. `{{memory}}` inserts the corresponding shared memory; `{{intelligence}}` inserts the current donation snapshot; the standalone `{{conversation}}` node preserves original conversation and tool-message roles.
- `auto-review-memory.md` and `chat-memory.md` are shown only when that memory has never been saved. Their initial revision is `0`. A saved empty body stays empty. The displayed initial text is the before-state for the first edit, diff and history entry.
- `tool-descriptions.json` supplies editable descriptions for the nine existing tools. Names, parameter schemas, available actions and authorization come from code. Descriptions are applied only after permission and enabled-tool filtering.

Existing saved message templates, mode switches and tool choices are preserved. Missing descriptions in older saved settings receive the built-in defaults. Settings GET returns all nine descriptions. Settings PUT accepts a partial `tool_descriptions` object: omitted names keep their current descriptions, an empty string clears one, and an omitted or null object preserves all. Unknown names are rejected. Each normalized description is limited to 16 KiB and the set to 256 KiB; line endings are stored as LF. A live tool loop uses current descriptions on its next model request.

The presets do not enable calls: automatic review remains off, chat remains closed, assistant prefills remain empty, and a configured model driver is still required. Administrators can edit templates, memories and descriptions through the existing settings pages. These texts guide the model; current server checks, resource versions and the final transaction remain authoritative.
