### Setting up MCP servers for Cursor

This guide explains how to configure Cursor to use MCP servers for Jira (Atlassian) and GitHub, based on an example configuration. It includes where the file should live and a safe, fake sample you can adapt.

### File location

- Global user config (recommended for secrets): `~/.cursor/mcp.json`
- Project-specific config (avoid committing secrets): `<repo>/.cursor/mcp.json`

For this repository, a project-level path would be `hypershift/.cursor/mcp.json`. If you keep secrets in the file, use the global user config instead and do not commit it.

### Prerequisites

- Podman or Docker installed (the example uses `podman`).
- Valid credentials for the services you enable.

### Example mcp.json (fake sample)

Copy this into your `~/.cursor/mcp.json` (or `hypershift/.cursor/mcp.json` if you are using a project-local config). Replace placeholder values with your own. Do not commit real secrets.

```json
{
  "mcpServers": {
    "atlassian": {
      "command": "podman",
      "args": [
        "run",
        "-i",
        "--rm",
        "-e", "JIRA_URL",
        "-e", "JIRA_USERNAME",
        "-e", "JIRA_API_TOKEN",
        "-e", "JIRA_PERSONAL_TOKEN",
        "-e", "JIRA_SSL_VERIFY",
        "ghcr.io/sooperset/mcp-atlassian:latest"
      ],
      "env": {
        "JIRA_URL": "https://issues.redhat.com/",
        "JIRA_USERNAME": "your_username",
        "JIRA_API_TOKEN": "ATATT-REDACTED-EXAMPLE-TOKEN",
        "JIRA_PERSONAL_TOKEN": "REDACTED-EXAMPLE-PERSONAL-TOKEN",
        "JIRA_SSL_VERIFY": "true"
      }
    },
    "github": {
      "url": "https://api.githubcopilot.com/mcp/",
      "headers": {
        "Authorization": "Bearer ghp_examplePERSONALACCESS-TOKEN-REDACTED"
      }
    }
  }
}
```

### Getting Tokens 
You'll need to generate your own tokens in several of these examples:

- For JIRA API TOKEN, use https://id.atlassian.com/manage-profile/security/api-tokens
- For JIRA PERSONAL TOKEN, use https://issues.redhat.com/secure/ViewProfile.jspa?selectedTab=com.atlassian.pats.pats-plugin:jira-user-personal-access-tokens
- For GitHub bearer token, use https://github.com/settings/tokens

### Notes and tips

- Do not commit real tokens. If you must keep a project-local file, prefer committing a `mcp.json.sample` with placeholders, and keep your real `mcp.json` untracked.
- The `atlassian` server example uses an MCP container image: `ghcr.io/sooperset/mcp-atlassian:latest`.
- If you prefer Docker, replace the `podman` command with `docker` (arguments are typically the same).
- If Podman is installed via Podman Machine on macOS, ensure it is running: `podman machine start`.
- Keep `JIRA_SSL_VERIFY` as "true" unless you have a specific reason to disable TLS verification.
- Limit active MCP servers: running too many at once can degrade performance or hit limits. Use Cursor's MCP panel to disable those you don't need for the current session.

### Validation

- After creating/updating `mcp.json`, restart Cursor.
- In Cursor, open the MCP panel and verify both `atlassian` and `github` servers appear and are healthy.
