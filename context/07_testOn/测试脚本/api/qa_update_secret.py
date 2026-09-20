# -*- coding: utf-8 -*-
"""QA 辅助：RSA 加密后更新集成密钥。用法：python qa_update_secret.py <新密钥>"""
import base64
import json
import sys
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
    with urllib.request.urlopen(req, timeout=15) as r:
        return json.loads(r.read().decode("utf-8"))


if __name__ == "__main__":
    new_secret = sys.argv[1]
    token = open("token.txt").read().strip()
    # 当前版本号（乐观锁）
    cur = call("GET", "/api/integration-secret", token=token)
    version = cur["data"]["version"]
    pk = call("GET", "/api/auth/public-key")["data"]
    cipher = rsa_encrypt(pk["publicKey"], new_secret)
    res = call(
        "POST",
        "/api/integration-secret/update",
        {"secret": cipher, "keyId": pk["keyId"], "version": version},
        token=token,
    )
    print(json.dumps(res, ensure_ascii=False))
