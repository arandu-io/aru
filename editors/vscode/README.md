# VS Code settings for generated projects

A `.kyse.go` is not Go. Without telling the editor so, gopls parses it, marks
every directive as a syntax error, and the file is red while being correct.

`aru new` embeds `settings.json` and copies it into the new project's `.vscode/`
directory. The settings associate `.kyse.go` with the `kyse` language, disable
format-on-save for those sources, and keep compiled views under
`storage/framework/views` out of search results. Editable sources under
`resources/views` remain visible.

## Official extension

The installable adapter lives in
[`arandu-io/vscode-arandu`](https://github.com/arandu-io/vscode-arandu). That
repository owns the TextMate grammar, language configuration, snippets, VS Code
manifest, project navigation, and the language client that starts `aru lsp`.

This directory deliberately carries only the project settings that must be
available inside the standalone `aru` binary. Keeping the installable assets in
one repository prevents the editor grammar and configuration from diverging.
