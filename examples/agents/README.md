# Wallet access for agents

Use Laevitas CLI **v0.12.0 or later** from [cli.laevitas.ch](https://cli.laevitas.ch).
Existing installations: `laevitas update`. Discovery and the fictional sample
need no wallet, key or payment.

```sh
laevitas agent catalog
laevitas agent sample
# On the computer running your agent; generates a new, dedicated wallet locally.
laevitas wallet init --generate
laevitas agent login
laevitas agent balance
```

For an existing wallet, configure `LAEVITAS_WALLET_KEY` through your local secret
manager or run `laevitas wallet init` in your own terminal. Never put a private key
or seed in chat. Use `LAEVITAS_CONFIG_DIR` to isolate different agents. Wallet
sign-in is free, proves ownership, and grants no live credits or dashboard account.
The native CLI supports Base EOA wallets. The HTTP API also accepts Solana Ed25519.

## Purchase once, then use the existing data commands

Fund the displayed wallet address with USDC on Base. Inspect the live catalog;
prices below illustrate the initial pack, not a permanent price guarantee.

```sh
laevitas agent quote credits-100
# Copy data.id from the successful quote response and check data.quote.amountCents.
laevitas agent pay CHECKOUT_ID --max-usdc 0.10
laevitas agent status CHECKOUT_ID
laevitas agent balance
LAEVITAS_AUTH=credits laevitas perps carry BTC-PERPETUAL -p 1h -n 1 -o json
laevitas agent usage
```

`quote` never spends. `pay` needs an explicit USDC ceiling and checks the saved
quote, origin, network and USDC asset before signing. After interruption, poll
`status`; pending/uncertain checkouts must not be replaced by a new payment.
Use `quote --request-id UUID` for a stable retry. Usage returns `nextBefore`; pass
it to `agent usage --before CURSOR` for the next page. `wallet credits` now reads
the authoritative balance. `agent account` shows the current paid entitlement;
`agent logout` revokes this device's management session.

Generated wallets use `credits` auth: data calls cannot automatically buy more.
For imported wallets, set `laevitas config set auth credits`. Existing explicit
`x402` auth keeps its automatic per-request payment behavior. Credit token/session
files are private and bound to API origin and wallet. Renew expired credit access
with `agent login`. The MCP connection renews wallet sign-in when needed.

REST and MCP draw credits according to the resource's published rules. Empty
responses and refused requests follow the API's refund policy. WebSocket passes,
exports and other products have separate prices; a REST credit is not a universal
service pass. The CLI's terminal dashboards use their existing REST/stream access.

## Agent Enterprise

```sh
laevitas agent quote agent-enterprise --cycle MONTHLY
# Pay with your approved ceiling after reviewing the returned price.
laevitas agent pay CHECKOUT_ID --max-usdc APPROVED_LIMIT
laevitas agent keys create --label research
laevitas agent keys list
laevitas agent keys revoke KEY_ID
```

Only an active Agent Enterprise subscription can create or use keys. The price is
25% below the same current human Enterprise billing period. Supported periods:
`MONTHLY`, `QUARTERLY`, `BIYEARLY`, `YEARLY`. Prepaid, no automatic renewal, no
human dashboard seats. The API applies its existing Enterprise history rules.
Keys are shown once in `data.key`; put them in local secret storage and supply
`LAEVITAS_API_KEY` with `LAEVITAS_AUTH=api-key`. Do not forward key creation output
to a chat room. Renew with a new quote; unused paid time is preserved.

## MCP clients

After signing in and purchasing credits, add [mcp.json](mcp.json) to your client's
MCP configuration. It launches `laevitas agent mcp` over stdio. The connection uses
existing credits or an Enterprise key, never automatically purchases credits, and
returns a structured refusal when access is unavailable. Use an absolute binary
path if your client does not inherit your terminal's PATH.

Remote clients may use `https://apiv2.laevitas.ch/api/v1/mcp` with an Enterprise
`X-API-Key`, or a valid wallet `X-Credit-Token`. Clients that cannot set headers or
perform wallet signing should use a trusted local CLI/MCP host. A plain chat
window is not a wallet. The HTTP protocol is documented in
[skill.md](https://apiv2.laevitas.ch/skill.md).

## Hermes and Telegram

[Hermes documents stdio MCP configuration and its Telegram connection](https://hermes-agent.nousresearch.com/docs/getting-started/quickstart).
Merge [hermes.yaml](hermes.yaml) into `~/.hermes/config.yaml`, with the CLI installed
on that same host. Restart Hermes, use `hermes tools` to enable the connection on
the intended platform, and test one free discovery call. For Telegram, run
`hermes gateway setup` and configure your bot token and allowed users privately.
Then start `hermes gateway`.

Example message: “Use Laevitas to show the latest BTC perpetual carry on Deribit.
Use existing credits only. If payment is needed, stop and show me the quote.”

For your own Telegram bot, [telegram_bot.py](telegram_bot.py) wraps a narrow set
of commands. Set `TELEGRAM_BOT_TOKEN` and `TELEGRAM_ALLOWED_CHAT_IDS` in the host's
private environment, finish wallet setup locally, then run the file. It accepts
`/catalog`, `/sample`, `/balance` and `/carry BTC-PERPETUAL`; it never exposes a
purchase or key-management command. No bot is deployed or connected by this repo.

## Muse and Grok Bot

[Muse](https://muse.ai/) provides a persistent Linux computer; [Grok Bot](https://docs.x.ai/grok-bot/overview)
provides a persistent computer with a terminal. Install the CLI in the agent's
computer where terminal access is available, using that product's secure sign-in
and approval controls for setup. Each host needs its own local credentials.
These are CLI workflows; no native Laevitas marketplace connector is claimed.

Give either agent this instruction:

> Read https://apiv2.laevitas.ch/skill.md and use the installed Laevitas CLI.
> Start with `laevitas agent catalog` and `laevitas agent sample`. For live data,
> use my configured wallet with `LAEVITAS_AUTH=credits`. Show my remaining balance,
> fetch one hour of BTC perpetual carry on Deribit, and report the source and UTC
> timestamp. Do not purchase, create keys, expose secrets, or change auth mode.

For a custom Grok/API chat application, expose [chat_tool.py](chat_tool.py) as a
function tool using [the official function-calling interface](https://docs.x.ai/developers/tools/function-calling).
The application runs the validated local function and returns its JSON result to
the model. Do not give model-generated text to a shell. The same wrapper works
with any model that supports function tools; purchases remain outside the tool.

## 6022 partner onboarding

See [partner-6022.md](partner-6022.md). This is a separate signed A2A protocol
connection. Partner wallet authorization never buys Laevitas market-data credits.

## Reference patterns

The discovery-first experience draws on [CoinGecko's public MCP](https://mcp.api.coingecko.com/),
[Glassnode's discovery and metadata flow](https://research.glassnode.com/glassnode-mcp-server-guide/),
and [Nansen's wallet CLI](https://github.com/nansen-ai/nansen-cli). Their products,
prices and entitlements are independent of Laevitas. These links are references,
not claimed integrations or partnerships. Reviewed 2026-11-04.
