# -*- coding: utf-8 -*-
"""API-CREATE 用例执行器：B1 发起手动定向分析校验矩阵。"""
import json
import os
import urllib.error
import urllib.request

BASE = "http://127.0.0.1:8080"
HERE = os.path.dirname(os.path.abspath(__file__))


def req(method, path, token=None, body=None):
    data = json.dumps(body).encode() if body is not None else None
    r = urllib.request.Request(BASE + path, data=data, method=method)
    if data:
        r.add_header("Content-Type", "application/json")
    if token:
        r.add_header("Authorization", "Bearer " + token)
    try:
        with urllib.request.urlopen(r, timeout=20) as resp:
            return resp.status, json.loads(resp.read().decode("utf-8"))
    except urllib.error.HTTPError as e:
        try:
            return e.code, json.loads(e.read().decode("utf-8"))
        except Exception:
            return e.code, {}


def dt(day_offset):
    import datetime
    return (datetime.date.today() + datetime.timedelta(days=day_offset)).isoformat()


def main():
    token = open(os.path.join(HERE, "token.txt")).read().strip()
    ok_body = {
        "target_mode": "specified",
        "staffs": [
            {"staff_id": "1", "staff_name": "李雪涛"},
            {"staff_id": "1", "staff_name": "王莹"},
        ],
        "period_start": dt(-7),
        "period_end": dt(-1),
    }
    cases = []
    cases.append(("ok-2p", dict(ok_body)))
    b = dict(ok_body); b["period_end"] = dt(0); cases.append(("end-today", b))
    b = dict(ok_body); b["period_end"] = dt(3); cases.append(("end-future", b))
    b = dict(ok_body); b["period_start"] = dt(-1); b["period_end"] = dt(-7); cases.append(("end-before-start", b))
    b = dict(ok_body); b["staffs"] = []; cases.append(("empty-staffs", b))
    b = dict(ok_body); b["staffs"] = [{"staff_id": "1"}]; cases.append(("no-name", b))
    b = dict(ok_body); b["period_start"] = "2026/09/01"; cases.append(("bad-date", b))
    b = dict(ok_body); b["target_mode"] = "xyz"; cases.append(("bad-mode", b))
    b = dict(ok_body)
    b["staffs"] = [
        {"staff_id": "1", "staff_name": "李雪涛"},
        {"staff_id": "1", "staff_name": " 李雪涛 "},
        {"staff_id": "1", "staff_name": "王莹"},
    ]
    cases.append(("dup-names", b))
    b = dict(ok_body); b["period_start"] = dt(-1); b["period_end"] = dt(-1); cases.append(("equal-days", b))
    b = dict(ok_body)
    b["staffs"] = [{"staff_id": "1", "staff_name": "  "}]
    cases.append(("blank-name", b))
    b = dict(ok_body); del b["period_end"]; cases.append(("miss-end", b))

    for tid, body in cases:
        st, r = req("POST", "/api/assessment/batches/create", token=token, body=body)
        d = r.get("data")
        extra = ""
        if isinstance(d, dict):
            extra = f"batch_no={d.get('batch_no')} status={d.get('status')} total={d.get('total_count')} id={d.get('id')}"
        print(f"{tid}\tHTTP={st}\tcode={r.get('code')}\tmsg={r.get('message','')}\t{extra}")


if __name__ == "__main__":
    main()
