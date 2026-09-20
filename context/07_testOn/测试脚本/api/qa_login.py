# -*- coding: utf-8 -*-
"""QA 辅助：RSA-OAEP 加密密码并登录，输出 token。用法：python qa_login.py <用户名> <密码>"""
import base64
import json
import sys
import urllib.request

BASE = "http://127.0.0.1:8080"


def post(path, payload, token=None):
    req = urllib.request.Request(
        BASE + path,
        data=json.dumps(payload).encode("utf-8"),
        headers={"Content-Type": "application/json"},
        method="POST",
    )
    if token:
        req.add_header("Authorization", "Bearer " + token)
    with urllib.request.urlopen(req, timeout=10) as r:
        return json.loads(r.read().decode("utf-8"))


def get(path, token=None):
    req = urllib.request.Request(BASE + path, method="GET")
    if token:
        req.add_header("Authorization", "Bearer " + token)
    with urllib.request.urlopen(req, timeout=10) as r:
        return json.loads(r.read().decode("utf-8"))


def login(username, password):
    pk = get("/api/auth/public-key")["data"]
    pem = pk["publicKey"]
    key_id = pk["keyId"]
    # 纯 stdlib RSA-OAEP：用 cryptography 不可用时降级提示
    try:
        from cryptography.hazmat.primitives import serialization
        from cryptography.hazmat.primitives.asymmetric import padding
        from cryptography.hazmat.primitives import hashes
    except ImportError:
        print("NEED_CRYPTOGRAPHY")
        return None
    pub = serialization.load_pem_public_key(pem.encode())
    cipher = pub.encrypt(
        password.encode("utf-8"),
        padding.OAEP(mgf=padding.MGF1(hashes.SHA256()), algorithm=hashes.SHA256(), label=None),
    )
    body = {
        "username": username,
        "passwordCipher": base64.b64encode(cipher).decode(),
        "keyId": key_id,
    }
    return post("/api/login", body)


if __name__ == "__main__":
    u = sys.argv[1]
    p = sys.argv[2]
    res = login(u, p)
    print(json.dumps(res, ensure_ascii=False)[:500])
    if res and res.get("code") == 0:
        with open("token.txt", "w") as f:
            f.write(res["data"]["token"])
        print("TOKEN_SAVED")
