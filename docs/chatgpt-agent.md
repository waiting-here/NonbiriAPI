# Connect ChatGPT to the AI workspace

An administrator can connect one ChatGPT account to an instance and use its available models for AI conversations and automatic reviews. Availability and usage limits depend on the account and OpenAI's current preview policy. The connection does not change the permissions of the person using the AI workspace.

For the separate built-in device-code source and its explicit per-person access
rules, see [Connect Codex](codex-agent.md). The connection and authorization
procedure below remain specific to the ChatGPT source.

## Connect from your computer

1. In the administrator AI settings, download the instance connection target.
2. On the computer where you will open the browser, build the local tool from this repository with Go 1.26:

   ```sh
   go build -o nonbiri-chatgpt-login ./cmd/nonbiri-chatgpt-login
   ./nonbiri-chatgpt-login --target instance-target.json --output connection.json
   ```

   On Windows, build `nonbiri-chatgpt-login.exe` and run that executable instead.
3. Open the printed link in a browser on the same computer. Sign in and approve **NonbiriAPI**. Keep the local tool running until it creates `connection.json`.
4. Import that file into the same instance's administrator page over HTTPS. The file is encrypted for that instance and can be imported once. Delete the transfer file after a successful import.
5. Add a ChatGPT driver, load the available models, select a model, and choose where to use that driver.

The tool opens a temporary `127.0.0.1` callback listener. It never needs a public callback port on the server. Its `nonbiri-chatgpt-host-id.txt` file contains an opaque local identifier; keep it for later authorization on this computer. The instance maintains its own host identifier. Access and refresh tokens are never printed or saved in plaintext by this tool.

The instance owns subsequent token refreshes. Reconnecting uses the saved registration for the same ChatGPT account. Download a fresh target before authorizing again; a target becomes stale when the connection is replaced, disconnected, or invalidated. Existing output files are not overwritten, so use a new output filename for a new attempt. An import failure preserves the existing connection.

To switch accounts, choose **Connect another account** when downloading the target, then run the local tool with that target. Your current connection stays available until the new authorization is successfully imported. A normal reconnect target continues to require the existing account.

## Choose a source and its defaults

Keep the existing Chat Completions driver, add an external Responses driver with its own API key, or use the connected ChatGPT account. A global default applies when no purpose or individual override is configured. Automatic review, administrator conversations, all steward conversations, and individual stewards can each select another driver. Removing an override restores inheritance.

Each dispatched request records its model, source, and settings. A settings change applies to subsequent requests. A failed connection or exhausted subscription allowance does not automatically send the request to another source.

ChatGPT requests use `store: false`, `stream: true`, and the Responses API. Optional model settings are omitted unless configured, allowing the selected model's server defaults to apply. Supported advanced overrides include reasoning effort, mode and context, text verbosity, parallel tool calls, service tier, and prompt cache key; support still depends on the selected model. The local context window and output reserve control history budgeting. They do not set a subscription model's maximum output. An external API driver can separately set an upstream output limit.

The adapter requests encrypted reasoning output for stateless continuation. It preserves the native response items for the same driver, endpoint, model, and ChatGPT connection. Normal token refresh preserves continuation. Switching account, source, or model carries over compatible messages and function calls; opaque reasoning items are not sent to the new source.

## Read connection and usage information

The connection page shows the access token expiry and the latest connection record update, including initial import, automatic refresh, disconnect, or invalidation. Refresh happens when a model or discovery request needs it. Reloading the status page only reads local state; it does not refresh credentials or call a model.

The public sign-in API does not expose the account's remaining subscription allowance or its reset time. The page marks that information unavailable and links to [ChatGPT usage settings](https://chatgpt.com/settings/usage). A reported usage-limit error is evidence of that failed request; it does not establish that the entire account allowance is exhausted.

Local usage covers requests completed and recorded in the last 24 hours whose request details and ChatGPT provider snapshot are still retained. It includes earlier account connections, model calls and compaction. Requests still running, old records without a provider snapshot, and deleted request details are excluded. Token counts include only reported usage; requests without usage are counted separately as unknown. Cached input and reasoning tokens are subsets of input and output respectively. These figures are not an account balance or usage across other applications. The latest recognized subscription-limit error is shown only within this same retained window. No private account API or background quota polling is used.

## Disconnect and recover

Disconnect stops active ChatGPT model requests and clears the locally stored session. The server also attempts to revoke the refresh token. If remote revocation cannot be confirmed, the page reports that separately; review the connection in your ChatGPT account if necessary. Registration metadata remains available for reconnecting.

A temporary network or upstream failure leaves the session available for retry. A rejected or expired refresh token requires a new local authorization and import. For account eligibility or usage-limit errors, follow the upstream account guidance. A stream that ends without `response.completed` is recorded as incomplete or failed.

The official preview currently does not provide host-specific usage attribution or host-specific revocation for transferred sessions. Supported fields and tools can change during preview. See OpenAI's [self-hosted instructions](https://developers.openai.com/siwc/token-sharing-open-source/self-hosted-vms), [registration flow](https://developers.openai.com/siwc/token-sharing-open-source/sign-in), [model and inference guide](https://developers.openai.com/siwc/token-sharing-open-source/models-and-inference), and [preview limitations](https://developers.openai.com/siwc/token-sharing-open-source/preview-limitations).
