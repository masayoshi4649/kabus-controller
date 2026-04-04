"""kabuステーションのWebSocket受信用テストスクリプト。

使い方:
    python kabus_ws_receive.py

依存関係:
    pip install websockets
"""

from __future__ import annotations

import asyncio
import json
from typing import Any

import websockets
from websockets.client import WebSocketClientProtocol
from websockets.exceptions import ConnectionClosed


# WS_URL = "ws://localhost:18080/kabusapi/websocket"
WS_URL = "ws://127.0.0.1:8080/kabusapi/websocket"


def format_message(message: str) -> str:
    """JSONなら見やすく整形し、それ以外はそのまま返す。"""
    try:
        payload: Any = json.loads(message)
    except json.JSONDecodeError:
        return message

    return json.dumps(payload, ensure_ascii=False, indent=2)


async def receive_messages(ws: WebSocketClientProtocol) -> None:
    """受信したメッセージを標準出力へ表示する。"""
    print(f"Connected: {WS_URL}")

    async for message in ws:
        print("=" * 80)
        print(format_message(message))


async def main() -> None:
    while True:
        try:
            async with websockets.connect(
                WS_URL, ping_interval=20, ping_timeout=20
            ) as ws:
                await receive_messages(ws)
        except ConnectionClosed as exc:
            print(f"Connection closed: code={exc.code}, reason={exc.reason}")
        except OSError as exc:
            print(f"Connection error: {exc}")

        print("Retrying in 3 seconds...")
        await asyncio.sleep(3)


if __name__ == "__main__":
    try:
        asyncio.run(main())
    except KeyboardInterrupt:
        print("Stopped")
