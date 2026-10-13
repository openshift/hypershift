# Adding Marketplace Plugins to Claude Code

This guide explains how to add new plugins from the openshift-eng/ai-helpers marketplace to this repository.

## Current Configuration

The repository is configured with the openshift-eng/ai-helpers marketplace in `.claude/settings.json`:

```json
{
  "extraKnownMarketplaces": {
    "openshift-ai-helpers": {
      "source": {
        "source": "github",
        "repo": "openshift-eng/ai-helpers"
      }
    }
  },
  "enabledPlugins": {
    "jira@ai-helpers": true,
    "utils@ai-helpers": true
  }
}
```

## Currently Enabled Plugins

- **jira** - Provides `/jira:solve` and `/jira:status-rollup` commands
- **utils** - Generic utilities plugin with various helper commands

## Adding a New Plugin

### Option 1: Auto-install for all team members

To automatically install a plugin for everyone who trusts the repository:

1. Check available plugins in the marketplace:
   ```bash
   /plugin marketplace browse ai-helpers
   ```

2. Edit `.claude/settings.json` and add the plugin to `enabledPlugins`:
   ```json
   "enabledPlugins": {
     "jira@ai-helpers": true,
     "utils@ai-helpers": true,
     "new-plugin@ai-helpers": true  // Add this line
   }
   ```

3. Commit the changes:
   ```bash
   git add .claude/settings.json
   git commit -m "Enable new-plugin from ai-helpers marketplace"
   ```

4. Team members will need to restart Claude Code or trust the folder again to pick up the new plugin.

### Option 2: Install manually (user-specific)

Users can install plugins without modifying the repository settings:

```bash
# List available plugins
/plugin marketplace browse ai-helpers

# Install a specific plugin
/plugin install plugin-name@ai-helpers
```

**Note:** Manual installations are stored in the user's local settings and won't affect other team members.

## Verifying Installation

After adding a plugin, verify it's working:

1. List configured marketplaces:
   ```bash
   /plugin marketplace list
   ```

2. Check installed plugins:
   ```bash
   /plugin
   ```

3. Test the plugin commands (they should auto-complete when you type `/`)

## Troubleshooting

### Plugin not appearing after adding to settings.json

1. **Verify the marketplace ID**: The marketplace name in the screenshot when you run `/plugin marketplace list` is "ai-helpers", so use that in the `enabledPlugins` (e.g., `jira@ai-helpers`)

2. **Restart Claude Code**: Exit and start a new session

3. **Check you've trusted the folder**: Claude Code will prompt you to trust the repository folder when you first open it

4. **Manual installation as fallback**: If auto-install fails, try manually installing:
   ```bash
   /plugin install jira@ai-helpers
   ```

### Finding the correct plugin name

The plugin name comes from the `name` field in the plugin's `.claude-plugin/plugin.json` file in the marketplace repository. You can view available plugins at:
https://github.com/openshift-eng/ai-helpers/tree/main/plugins

## Adding Plugins from Other Marketplaces

To add a completely different marketplace:

1. Add it to `extraKnownMarketplaces` in `.claude/settings.json`:
   ```json
   "extraKnownMarketplaces": {
     "openshift-ai-helpers": {
       "source": {
         "source": "github",
         "repo": "openshift-eng/ai-helpers"
       }
     },
     "another-marketplace": {
       "source": {
         "source": "github",
         "repo": "org/repo-name"
       }
     }
   }
   ```

2. Enable plugins from the new marketplace:
   ```json
   "enabledPlugins": {
     "jira@ai-helpers": true,
     "utils@ai-helpers": true,
     "plugin-name@another-marketplace": true
   }
   ```

## References

- [Claude Code Plugin Marketplace Documentation](https://docs.claude.com/en/docs/claude-code/plugin-marketplaces)
- [openshift-eng/ai-helpers Repository](https://github.com/openshift-eng/ai-helpers)
- Project Documentation: [CLAUDE.md](../../CLAUDE.md)
