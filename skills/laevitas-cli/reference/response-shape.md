# REST response shape & error codes

Every REST JSON response uses a stable envelope. Parse `.success` first, then `.data` (success) or `.error` (failure). Errors are written to **stdout** under `-o json` and the process exits non-zero — so one jq pipeline covers both branches.

## Success

```json
{
  "success": true,
  "data": [ ... ],
  "meta": {
    "next_cursor": "...",
    "count": 100,
    "auth": "api-key",
    "credits_remaining": 950,
    "latency_ms": 247
  }
}
```

## Failure

```json
{
  "success": false,
  "error": {
    "message": "API key invalid or expired...",
    "code": "AUTH_INVALID",
    "status": 401,
    "endpoint": "/api/v1/perpetuals/carry"
  }
}
```

## Stable error codes

Branch on `.error.code` (stable), never on `.error.message` (prose, may change between versions).

| Code | When |
|------|------|
| `AUTH_INVALID` | 401 — API key missing/invalid |
| `AUTH_FORBIDDEN` | 403 — wrong tier or revoked access |
| `RATE_LIMITED` | 429 — back off, retry with delay |
| `PAYMENT_REQUIRED` | 402 — x402 payment needed and no wallet path attempted |
| `WALLET_NOT_CONFIGURED` | 402 — payment needed, wallet path requested, but no key set. Set `LAEVITAS_WALLET_KEY` or run `laevitas wallet set-key 0x<hex>`. |
| `INSUFFICIENT_BALANCE` | 402 — wallet exists but lacks USDC on Base. The envelope includes `wallet_address`; fund it. |
| `PAYMENT_REJECTED` | 4xx after signing — server bounced the signed payment. Verify wallet config; not retryable without intervention. |
| `BAD_REQUEST` | 4xx — fix params and retry |
| `NOT_FOUND` | 404 — instrument or path doesn't exist (often a guessed instrument name — use `catalog`) |
| `SERVER_ERROR` | 5xx — transient, retry with backoff |
| `NETWORK_ERROR` | DNS/TCP/timeout — retry with backoff |
| `UNKNOWN_ERROR` | fallback |

## Reading data

All field paths go through `.data`. Never read top-level array paths like `.[0]`.

```bash
# first futures mark price
laevitas futures snapshot --currency BTC -o json | jq '.data[0].mark_price'

# total record count from meta
laevitas perps carry BTC-PERPETUAL -p 7d -o json | jq '.meta.count'

# which auth path served this request
laevitas perps carry BTC-PERPETUAL -n 1 -o json | jq -r '.meta.auth'   # api-key | credit | on-chain
```

## Output modes

- `-o json` — enveloped, the agent default.
- `-o table` / `-o csv` — **not** enveloped; they format `.data` directly for human/spreadsheet use.
- `-o auto` (default) — table if stdout is a TTY, JSON if piped.

Always pass `-o json` explicitly in scripts so behaviour doesn't depend on whether a TTY is attached.
