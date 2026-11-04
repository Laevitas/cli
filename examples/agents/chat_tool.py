"""A small read-only tool adapter. The owner configures the CLI before use."""
import json
import os
import subprocess

TOOL = {
    "name": "laevitas_read",
    "description": "Read the product catalog, free sample, balance or recent perpetual carry using existing access.",
    "parameters": {"type": "object", "properties": {
        "action": {"type": "string", "enum": ["catalog", "sample", "balance", "carry"]},
        "instrument": {"type": "string", "enum": ["BTC-PERPETUAL", "ETH-PERPETUAL"]}
    }, "required": ["action"], "additionalProperties": False}
}


def laevitas_read(action, instrument="BTC-PERPETUAL"):
    if not isinstance(action, str) or not isinstance(instrument, str):
        return {"success": False, "error": {"code": "INVALID_ARGUMENT", "message": "Use string arguments"}}
    commands = {"catalog": ["agent", "catalog"], "sample": ["agent", "sample"],
                "balance": ["agent", "balance"], "carry": ["perps", "carry", instrument, "-p", "1h", "-n", "1", "-o", "json"]}
    if action not in commands or instrument not in ["BTC-PERPETUAL", "ETH-PERPETUAL"]:
        return {"success": False, "error": {"code": "INVALID_ARGUMENT", "message": "Unsupported query"}}
    env = dict(os.environ)
    # Owner-controlled Enterprise credentials are allowed; otherwise spend only
    # existing credits, regardless of the CLI's saved automatic payment setting.
    env["LAEVITAS_AUTH"] = "api-key" if env.get("LAEVITAS_API_KEY") else "credits"
    try:
        result = subprocess.run(["laevitas", *commands[action]], shell=False, capture_output=True,
                                text=True, timeout=40, env=env)
        response = json.loads(result.stdout)
        if not isinstance(response, dict) or not isinstance(response.get("success"), bool): raise ValueError()
        return response
    except (OSError, subprocess.TimeoutExpired, ValueError):
        return {"success": False, "error": {"code": "UNAVAILABLE", "message": "Check the local CLI setup and agent balance."}}
