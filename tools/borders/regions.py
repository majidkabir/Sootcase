"""Belgium's three regions from Statbel's statistical sectors.

Reads Statbel's statistical sectors GeoJSON (EPSG:3812, Belgian Lambert
2008), merges the sectors of each region (cd_rgn_refnis) into one outline,
simplifies it to SIMPLIFY_M metres and writes them in WGS 84 (EPSG:4326)
as GeoJSON with an ISO 3166-2 "code" property, for tools/borders -key code:

    python3 -I tools/borders/regions.py sh_statbel_statistical_sectors_3812_20260101.geojson be_regions.geojson
    go run ./tools/borders -in be_regions.geojson -key code -out pkg/borders/data/regions.bin.gz

Source: Statbel (Directorate-General Statistics - Statistics Belgium),
statistical sectors 2026 (situation 1 January 2026), CC BY 4.0,
https://statbel.fgov.be/en/open-data/statistical-sectors-2026 . The
sectors are drawn on 1:10,000 maps. Needs shapely and pyproj.
"""
import json
import sys

import numpy as np
import shapely
from pyproj import Transformer
from shapely.geometry import mapping, shape
from shapely.ops import unary_union

REGIONS = {"02000": "BE-VLG", "03000": "BE-WAL", "04000": "BE-BRU"}
SIMPLIFY_M = 2.0


def main(src, dst):
    with open(src, encoding="utf-8") as f:
        fc = json.load(f)
    parts = {code: [] for code in REGIONS.values()}
    for feat in fc["features"]:
        rgn = feat["properties"]["cd_rgn_refnis"]
        if rgn not in REGIONS:
            raise SystemExit(f"unknown region {rgn!r} in sector {feat['properties'].get('cd_sector')}")
        parts[REGIONS[rgn]].append(shape(feat["geometry"]).buffer(0))
    t = Transformer.from_crs("EPSG:3812", "EPSG:4326", always_xy=True)

    def to_wgs84(xy):
        return np.column_stack(t.transform(xy[:, 0], xy[:, 1]))

    out = []
    for code, geoms in parts.items():
        merged = unary_union(geoms).simplify(SIMPLIFY_M, preserve_topology=True)
        out.append({"type": "Feature", "properties": {"code": code, "sectors": len(geoms),
                    "area_km2": round(merged.area / 1e6, 3)},
                    "geometry": mapping(shapely.transform(merged, to_wgs84))})
        print(f"{code}: {len(geoms)} sectors, {merged.area / 1e6:.1f} km2, {merged.geom_type}", file=sys.stderr)
    with open(dst, "w", encoding="utf-8") as f:
        json.dump({"type": "FeatureCollection", "features": out}, f)


if __name__ == "__main__":
    main(sys.argv[1], sys.argv[2])
