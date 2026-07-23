#!/usr/bin/env python3
"""Fake Ollama /api/chat for tomobit perception.

Deterministic extraction: scans the user message for language keywords and
returns the perception JSON tomobit's Ollama extractor expects. No LLM.
"""
import json
import re
from http.server import BaseHTTPRequestHandler, HTTPServer

PORT = 11499

LANGS = [("rustlang", "rust"), ("golang", "go"), ("pylang", "python")]


class H(BaseHTTPRequestHandler):
    def do_POST(self):
        n = int(self.headers.get("Content-Length", 0))
        body = json.loads(self.rfile.read(n))
        user = ""
        for m in body.get("messages", []):
            if m.get("role") == "user":
                user = m.get("content", "")
        lang = ""
        for kw, val in LANGS:
            if kw in user:
                lang = val
                break
        out = {"lang": lang, "framework": "", "topic": "", "size": ""}
        resp = json.dumps({"message": {"content": json.dumps(out)}}).encode()
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(resp)))
        self.end_headers()
        self.wfile.write(resp)

    def log_message(self, *a):
        pass


if __name__ == "__main__":
    HTTPServer(("127.0.0.1", PORT), H).serve_forever()
