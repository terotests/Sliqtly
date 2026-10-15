# Homebrew tap for Sliqtly

Sliqtly Personal for macOS, the full install: the MCP server your AI agent
writes presentations through, and the editor and player in your browser.

```sh
brew install terotests/sliqtly/sliqtly
brew services start sliqtly
```

Then open http://localhost:8080/ (the editor) and connect your agent to
`http://localhost:8080/mcp`. Settings, networks, ports and firewalls:
https://sliqtly.com/local.html

Free for personal use and qualifying solo businesses with annual gross
revenue below €200,000. All other commercial use requires a commercial
license. The full license is installed with the formula
(`$(brew --prefix sliqtly)/LICENSE`).

`Formula/sliqtly.rb` is written by `.github/workflows/update.yml`; the
source of these files is `mcp-go/packaging/homebrew/` in Sliqtly.
