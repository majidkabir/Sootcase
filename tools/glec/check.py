#!/usr/bin/env python3
"""Checks pkg/factors/data/glec-3.3.json against the GLEC Framework v3.3 PDF (not in this repo).

Every value read from a Module 2 table must appear, as the WTT and TTW pair, on the table's pages in the
PDF's text (pdftotext). Derived values (Asia/Africa and temperature-controlled uplifts, the EU rail average's
split) are skipped; their base rows are checked. Rows whose text the PDF lays out out of order are listed
with what was checked by eye instead.

Run: python3 tools/glec/check.py GLEC_FRAMEWORK_V3.3.pdf
"""
import json, os, re, subprocess, sys

BY_EYE = {  # page text out of order; checked on the page image (p. 112)
    "glec/sea/vehicle_carrier/30000-49999_gt/hfo": "7.1 39.9", "glec/sea/vehicle_carrier/30000-49999_gt/mdo": "7.2 38.7"}
pages = subprocess.run(["pdftotext", "-layout", sys.argv[1], "-"], capture_output=True, text=True, check=True).stdout.split("\f")
norm = lambda t: re.sub(r"\s+", " ", t.replace(",", ""))
P = lambda *ns: " ".join(norm(pages[n - 1]) for n in ns)
here = os.path.dirname(os.path.abspath(__file__))
d = json.load(open(os.path.join(here, "..", "..", "pkg", "factors", "data", "glec-3.3.json")))


def forms(x):
    out = {"%g" % x, f"{x:.1f}", f"{x:.2f}"}
    if x == int(x):
        out.add(str(int(x)))
    return out


def pair(text, a, b, before=""):
    return any(re.search(r"(?<![\d.])" + re.escape(before) + re.escape(fa) + " " + re.escape(fb) + r"(?![\d.])", text)
               for fa in forms(a) for fb in forms(b))


ranges = {"glec/road/eu_sa/": (101, 102, 103), "glec/road/na/": (100,), "glec/road/cn/": (104, 105, 106),
          "glec/road/in/": (107, 108), "glec/rail/eu/diesel/": (98,), "glec/air/": (95,), "glec/iww/": (96,),
          "glec/sea/container/": (113, 114)}
bad, checked = [], 0
for l in d["legs"]:
    vt, w, t = l["vehicle_type"], l["wtt_g_per_tkm"], l["ttw_g_per_tkm"]
    if "/asia_africa/" in vt or "temperature_controlled" in vt or vt == "glec/rail/eu/average_traction_unknown" or vt in BY_EYE:
        continue
    if vt.startswith("glec/road/europe/electric/"):  # kWh/t-km x 334.08 g/kWh
        checked += 1
        kwh = round(w / (92.8 * 3.6), 4)
        if not re.search(r"(?<![\d.])(" + "|".join(map(re.escape, forms(kwh))) + r")(?![\d.])", P(103)):
            bad.append(f"{vt}: {kwh} kWh/t-km")
        continue
    checked += 1
    if vt in ("glec/rail/na/average", "glec/rail/in/average"):
        flow = norm(subprocess.run(["pdftotext", "-f", "98", "-l", "98", sys.argv[1], "-"], capture_output=True, text=True).stdout)
        ok = f"WTT = {w:g} and TTW = {t:g}" in flow
    elif vt.startswith("glec/rail/eu/electric/"):
        ok = re.search(r"(?<![\d.])(" + "|".join(map(re.escape, forms(w))) + r")(?![\d.])", P(99)) is not None
    elif vt.startswith("glec/sea/container/"):
        ok = pair(P(113, 114), round(w * 10, 3), round(t * 10, 3))
    elif vt.startswith("glec/sea/"):
        fuel = vt.rsplit("/", 1)[1].upper()
        ok = pair(P(109, 110, 111, 112), w, t, fuel + " ")
    else:
        txt = P(*next(v for k, v in ranges.items() if vt.startswith(k)))
        ok = re.search(re.escape("%g" % w) + r" - " + re.escape("%g" % w), txt) is not None if (t == 0 and "/cn/" in vt) else pair(txt, w, t)
    if not ok:
        bad.append(f"{vt}: {w} {t}")
hub_text = P(97)
for h in d["hubs"]:
    checked += 1
    if "%g" % ((h.get("wtw_g_per_tonne") or h.get("wtw_g_per_container", 0)) / 1000) not in hub_text:
        bad.append(f"hub {h['hub_type']}")
# Module 1 fuels: per kg, TTW and WTW (base tables) or WTT, TTW and WTW (blends), as printed.
fuel_text = P(*range(77, 91))
for f in d["fuels"]:
    if not f["fuel"].endswith("/kg"):
        continue
    checked += 1
    ttw, wtt = f["ttw_kg_per_unit"], f["wtt_kg_per_unit"]
    wtw = round(ttw + wtt, 4)
    blend = "/fuel/eu/diesel_" in f["fuel"] or "/fuel/eu/gasoline_" in f["fuel"] or "/fuel/na/diesel_" in f["fuel"] or "/fuel/na/gasoline_" in f["fuel"]
    ok = pair(fuel_text, ttw, wtw)  # blends: WTT = WTW - TTW, the total as printed
    if not ok:
        bad.append(f"{f['fuel']}: {ttw} {wtw}")
print(f"{checked} values checked against the PDF, {len(bad)} not found")
for b in bad:
    print("  NOT FOUND", b)
sys.exit(1 if bad else 0)
