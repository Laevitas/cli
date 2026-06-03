# Authentication

Two REST auth paths; WebSocket is API-key only today.

## API key

```bash
LAEVITAS_API_KEY=<key> laevitas …          # env override (highest priority)
laevitas config set api_key <key>          # persist to ~/.config/laevitas/config.json
laevitas config init                       # interactive setup
```

Required for `ws` streaming.

## x402 wallet (pay-per-request, USDC on Base)

```bash
LAEVITAS_WALLET_KEY=0x<hex> laevitas …     # env override
laevitas wallet set-key 0x<hex>            # persist
laevitas wallet show -o json               # inspect state without spending credits
```

Each request triggers an on-chain payment if no credit token is cached. After the first payment the API issues a JWT credit token cached at `~/.config/laevitas/x402-token` and reused until it expires. **REST only** — calling `laevitas ws` on the wallet path returns a clear error; switch to API-key auth for streaming.

## Choosing the path

`LAEVITAS_AUTH=auto|api-key|x402` controls preference when both are configured. Default `auto` = API key first, wallet fallback on 401/402.

Read `.meta.auth` on every response to confirm which path served it: `api-key`, `credit` (cached x402 token), or `on-chain` (fresh payment).

## Budget-aware loop (wallet path)

Self-throttle by reading `.meta.credits_remaining` after each request and handling the 402-family error codes:

```bash
export LAEVITAS_WALLET_KEY=0x...
BUDGET=100

while …; do
  RESP=$(laevitas perps carry BTC-PERPETUAL -p 1h -o json)
  if [ "$(echo "$RESP" | jq -r '.success')" = "false" ]; then
    case "$(echo "$RESP" | jq -r '.error.code')" in
      INSUFFICIENT_BALANCE)  echo "fund $(echo "$RESP" | jq -r '.error.wallet_address')"; exit 2 ;;
      WALLET_NOT_CONFIGURED) echo "set LAEVITAS_WALLET_KEY"; exit 2 ;;
      RATE_LIMITED)          sleep 2; continue ;;
      *)                     echo "$RESP" | jq -r '.error.message'; exit 1 ;;
    esac
  fi

  echo "$RESP" | jq '.data[0]'
  remaining=$(echo "$RESP" | jq -r '.meta.credits_remaining // empty')
  if [ -n "$remaining" ] && [ "$remaining" -lt "$BUDGET" ]; then
    echo "credits below threshold ($remaining); stopping"; break
  fi
done
```

`laevitas doctor` validates auth locally (it decodes the wallet key without dialing chain) and never costs money.
