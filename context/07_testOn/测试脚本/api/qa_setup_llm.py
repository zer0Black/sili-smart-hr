# -*- coding: utf-8 -*-
"""QA 辅助：创建并启用 LLM 配置（排他启用）。用法：python qa_setup_llm.py"""
import base64
import json
import os
import urllib.request

from cryptography.hazmat.primitives import hashes, serialization
from cryptography.hazmat.primitives.asymmetric import padding

BASE = "http://127.0.0.1:8080"


def rsa_encrypt(pem, text):
    pub = serialization.load_pem_public_key(pem.encode())
    return base64.b64encode(
        pub.encrypt(
            text.encode("utf-8"),
            padding.OAEP(mgf=padding.MGF1(hashes.SHA256()), algorithm=hashes.SHA256(), label=None),
        )
    ).decode()


def call(method, path, payload=None, token=None):
    data = json.dumps(payload).encode() if payload is not None else None
    req = urllib.request.Request(BASE + path, data=data, method=method)
    if data:
        req.add_header("Content-Type", "application/json")
    if token:
        req.add_header("Authorization", "Bearer " + token)
    with urllib.request.urlopen(req, timeout=30) as r:
        return json.loads(r.read().decode("utf-8"))


if __name__ == "__main__":
    token = open(os.path.join(os.path.dirname(__file__), "token.txt")).read().strip()
    api_key = os.environ["ANTHROPIC_AUTH_TOKEN"]
    pk = call("GET", "/api/auth/public-key")["data"]
    body = {
        "name": "QA测试模型",
        "provider": "anthropic",
        "model_id": "glm-5.3-flash",
        "api_url": "http://10.10.10.36:3000",
        "api_key": rsa_encrypt(pk["publicKey"], api_key),
        "keyId": pk["keyId"],
    }
    created = call("POST", "/api/llm-configs/create", body, token=token)
    print("create:", json.dumps(created, ensure_ascii=False))
    if created["code"] != 0:
        raise SystemExit(1)
    llm_id = created["data"]["id"]
    enabled = call("POST", "/api/llm-configs/enable", {"id": llm_id}, token=token)
    print("enable:", json.dumps(enabled, ensure_ascii=False))
