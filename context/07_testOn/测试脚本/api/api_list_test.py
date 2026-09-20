# -*- coding: utf-8 -*-
"""API-LIST 用例执行器：批次列表查询与筛选。输出逐参数行结果到 stdout。"""
import json
import os
import sys
import urllib.error
import urllib.request

BASE = "http://127.0.0.1:8080"
HERE = os.path.dirname(os.path.abspath(__file__))


def req(method, path, qs="", token=None, body=None):
    url = BASE + path + (("?" + qs) if qs else "")
    data = json.dumps(body).encode() if body is not None else None
    r = urllib.request.Request(url, data=data, method=method)
    if data:
        r.add_header("Content-Type", "application/json")
    if token:
        r.add_header("Authorization", "Bearer " + token)
    try:
        with urllib.request.urlopen(r, timeout=15) as resp:
            return resp.status, json.loads(resp.read().decode("utf-8"))
    except urllib.error.HTTPError as e:
        try:
            return e.code, json.loads(e.read().decode("utf-8"))
        except Exception:
            return e.code, {}


def main():
    token = open(os.path.join(HERE, "token.txt")).read().strip()
    results = []

    # default
    st, b = req("GET", "/api/assessment/batches", token=token)
    results.append(("default", st, b))
    # trig-sch / trig-manual / trig-bad
    for tid, qs in [
        ("trig-sch", "trigger_type=scheduled"),
        ("trig-manual", "trigger_type=manual"),
        ("trig-bad", "trigger_type=xyz"),
        ("st-running", "status=running"),
        ("st-success", "status=success"),
        ("st-partial", "status=partial_failed"),
        ("st-failed", "status=failed"),
        ("st-bad", "status=xyz"),
        ("size-101", "page_size=101"),
        ("size-0", "page_size=0"),
        ("size-100", "page_size=100"),
        ("size-20", "page_size=20"),
        ("page-0", "page=0"),
        ("page-bad", "page=abc"),
    ]:
        st, b = req("GET", "/api/assessment/batches", qs=qs, token=token)
        results.append((tid, st, b))
    # noauth
    st, b = req("GET", "/api/assessment/batches")
    results.append(("noauth", st, b))

    # 摘要输出（list 行内容截断）
    for tid, st, b in results:
        d = b.get("data")
        if isinstance(d, dict) and "list" in d:
            rows = d["list"]
            desc = f"total={d.get('total')} page={d.get('page')} page_size={d.get('page_size')} rows={len(rows)}"
            if rows:
                first = rows[0]
                desc += f" first={{id:{first.get('id')},batch_no:{first.get('batch_no')},trigger:{first.get('trigger_type')},status:{first.get('status')},stalled:{first.get('stalled')}}}"
        else:
            desc = json.dumps(d, ensure_ascii=False)[:80] if d is not None else "null"
        print(f"{tid}\tHTTP={st}\tcode={b.get('code')}\t{desc}")

    # 倒序断言：取 default 行的 triggered_at 序列
    st, b = req("GET", "/api/assessment/batches", qs="page_size=50", token=token)
    rows = b["data"]["list"]
    ts = [r["triggered_at"] for r in rows]
    print("DESC_ORDER\t", "PASS" if ts == sorted(ts, reverse=True) else "FAIL", ts[:5] if ts else "(空)")


if __name__ == "__main__":
    main()
