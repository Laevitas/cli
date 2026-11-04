"""Optional Telegram example. Running it explicitly starts the bot; importing it does not."""
import json
import os
from urllib.request import Request, urlopen
from chat_tool import laevitas_read


def main():
    token = os.environ["TELEGRAM_BOT_TOKEN"]
    allowed = {int(value.strip()) for value in os.environ["TELEGRAM_ALLOWED_CHAT_IDS"].split(",") if value.strip()}
    if not allowed: raise ValueError("Configure at least one allowed chat ID")
    def telegram(method, data):
        request = Request("https://api.telegram.org/bot" + token + "/" + method,
                          json.dumps(data).encode(), {"Content-Type": "application/json"})
        with urlopen(request, timeout=40) as response: return json.load(response)
    offset = 0
    while True:
        updates = telegram("getUpdates", {"offset": offset, "timeout": 25, "allowed_updates": ["message"]})
        for update in updates.get("result", []):
            offset = update["update_id"] + 1
            message = update.get("message", {})
            chat = message.get("chat", {}).get("id")
            if chat not in allowed: continue
            words = message.get("text", "").split()
            action = words[0].split("@", 1)[0].removeprefix("/") if words else ""
            result = laevitas_read(action, words[1] if len(words) == 2 else "BTC-PERPETUAL") if len(words) <= 2 else {"success": False, "error": {"message": "Use /catalog, /sample, /balance or /carry BTC-PERPETUAL"}}
            telegram("sendMessage", {"chat_id": chat, "text": json.dumps(result, ensure_ascii=False)[:3900]})


if __name__ == "__main__":
    try: main()
    except Exception:
        # Network exceptions can contain the bot token in the request URL.
        raise SystemExit("Bot stopped. Check its private token, allowlist and local CLI setup.") from None
