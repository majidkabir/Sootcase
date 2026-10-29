#!/usr/bin/env python3
"""Builds pkg/factors/data/glec-3.3.json from the GLEC Framework v3.3 default values.

Values are transcribed from: Smart Freight Centre. Global Logistics Emissions Council Framework
for Logistics Emissions Accounting and Reporting; v3.3 edition, revised and updated (October 2026).
ISBN 978-90-833629-0-8. Section 3, Module 2 (default fuel efficiency and GHG emission intensity
values). Used with the written permission of Smart Freight Centre, which is acknowledged as the source.

Country continents (for GLEC's regions and trade lanes) come from OurAirports' countries.csv
(public domain), saved next to this script.

Run: python3 tools/glec/build.py   (writes the dataset; the test suite checks it parses)
"""
import csv
import json
import os

HERE = os.path.dirname(os.path.abspath(__file__))
OUT = os.path.join(HERE, "..", "..", "pkg", "factors", "data", "glec-3.3.json")
YEAR = 2026
GLEC = "GLEC Framework v3.3 (Smart Freight Centre, 2026)"

legs = []


def leg(mode, vt, wtt, ttw, source, basis):
    legs.append({"mode": mode, "vehicle_type": vt, "ttw_g_per_tkm": round(ttw, 4), "wtt_g_per_tkm": round(wtt, 4),
                 "source": source, "year": YEAR, "distance_basis": basis})


# ---------------------------------------------------------------- road
ROAD_SFD = "includes GLEC's +5% distance conversion, so it is per shortest feasible distance"

# Table 7-9, Europe and South America (HBEFA-based). (id, label, wtt, ttw)
EU_SA = [
    ("van_3_5t/diesel", "Table 7: van < 3.5 t, diesel (load 40%, empty 9%)", 195, 647),
    ("van_3_5t/petrol", "Table 7: van < 3.5 t, petrol (load 30%, empty 9%)", 244, 763),
    ("van_3_5t/cng", "Table 7: van < 3.5 t, CNG (load 40%, empty 9%)", 249, 643),
    ("van_3_5t/lpg", "Table 7: van < 3.5 t, LPG (load 40%, empty 9%)", 246, 713),
    ("rigid_3_5_7_5t/diesel", "Table 8: rigid truck 3.5-7.5 t GVW, average/mixed load, diesel", 78, 258),
    ("rigid_3_5_7_5t/cng", "Table 8: rigid truck 3.5-7.5 t GVW, average/mixed load, CNG", 88, 229),
    ("rigid_7_5_12t/diesel", "Table 8: rigid truck 7.5-12 t GVW, average/mixed load, diesel", 52, 172),
    ("rigid_7_5_12t/cng", "Table 8: rigid truck 7.5-12 t GVW, average/mixed load, CNG", 58, 153),
    ("rigid_12_20t/diesel", "Table 8: rigid truck 12-20 t GVW, average/mixed load, diesel", 44, 147),
    ("rigid_12_20t/cng", "Table 8: rigid truck 12-20 t GVW, average/mixed load, CNG", 51, 130),
    ("rigid_20_26t/diesel", "Table 8: rigid truck 20-26 t GVW, average/mixed load, diesel", 32, 107),
    ("rigid_20_26t/cng", "Table 8: rigid truck 20-26 t GVW, average/mixed load, CNG", 38, 98),
    ("rigid_20_26t/lng", "Table 8: rigid truck 20-26 t GVW, average/mixed load, LNG", 48, 107),
    ("rigid_26_32t/diesel", "Table 8: rigid truck 26-32 t GVW, average/mixed load, diesel", 29, 96),
    ("rigid_26_32t/container/diesel", "Table 8: rigid truck 26-32 t GVW, container, diesel", 29, 95),
    ("artic_34t/diesel", "Table 8: artic truck up to 34 t GVW, average/mixed load, diesel", 29, 95),
    ("artic_34t/container/diesel", "Table 8: artic truck up to 34 t GVW, container, diesel", 29, 95),
    ("artic_34_40t/diesel", "Table 8: artic truck 34-40 t GVW, average/mixed load, diesel", 23, 78),
    ("artic_34_40t/container/diesel", "Table 8: artic truck 34-40 t GVW, container, diesel", 23, 78),
    ("artic_34_40t_si/lng", "Table 8: artic truck 34-40 t GVW SI engine, average/mixed load, LNG", 32, 73),
    ("artic_34_40t_si/cng", "Table 8: artic truck 34-40 t GVW SI engine, average/mixed load, CNG", 26, 72),
    ("artic_34_40t_si/bio_lng", "Table 8: artic truck 34-40 t GVW SI engine, average/mixed load, bio-LNG", 35, 3),
    ("artic_34_40t_si/container/lng", "Table 8: artic truck 34-40 t GVW SI engine, container, LNG", 33, 72),
    ("artic_34_40t_si/container/cng", "Table 8: artic truck 34-40 t GVW SI engine, container, CNG", 26, 72),
    ("artic_34_40t_si/container/bio_lng", "Table 8: artic truck 34-40 t GVW SI engine, container, bio-LNG", 35, 3),
    ("artic_34_40t_hpdi/lng_diesel", "Table 8: artic truck 34-40 t GVW HPDI, average/mixed load, LNG/diesel", 28, 65),
    ("artic_34_40t_hpdi/cng_diesel", "Table 8: artic truck 34-40 t GVW HPDI, average/mixed load, CNG/diesel", 23, 64),
    ("artic_34_40t_hpdi/bio_lng_diesel", "Table 8: artic truck 34-40 t GVW HPDI, average/mixed load, bio-LNG/diesel", 31, 9),
    ("artic_34_40t_hpdi/container/lng_diesel", "Table 8: artic truck 34-40 t GVW HPDI, container, LNG/diesel", 28, 65),
    ("artic_34_40t_hpdi/container/cng_diesel", "Table 8: artic truck 34-40 t GVW HPDI, container, CNG/diesel", 23, 65),
    ("artic_34_40t_hpdi/container/bio_lng_diesel", "Table 8: artic truck 34-40 t GVW HPDI, container, bio-LNG/diesel", 31, 9),
    ("artic_40t_light_trailer/heavy/diesel", "Table 9: artic truck 40 t GVW incl. lightweight trailer, heavy load, diesel", 19, 62),
    ("artic_40_44t/light/diesel", "Table 9: artic truck 40-44 t GVW, light load, diesel", 33, 110),
    ("artic_40_44t/diesel", "Table 9: artic truck 40-44 t GVW, average/mixed load, diesel", 21, 69),
    ("artic_40_44t/heavy/diesel", "Table 9: artic truck 40-44 t GVW, heavy load, diesel", 18, 59),
    ("artic_40_44t/container/diesel", "Table 9: artic truck 40-44 t GVW, container, diesel", 21, 69),
    ("artic_60t/diesel", "Table 9: artic truck up to 60 t GVW, average/mixed load, diesel", 16, 54),
    ("artic_60t/heavy/diesel", "Table 9: artic truck up to 60 t GVW, heavy load, diesel", 14, 46),
    ("artic_60t/container/diesel", "Table 9: artic truck up to 60 t GVW, container, diesel", 16, 54),
    ("artic_72t/heavy/diesel", "Table 9: artic truck up to 72 t GVW, heavy load, diesel", 11, 36),
    ("artic_72t/container/diesel", "Table 9: artic truck up to 72 t GVW, container, diesel", 13, 42),
]
for vid, label, wtt, ttw in EU_SA:
    leg("road", "glec/road/eu_sa/" + vid, wtt, ttw,
        f"{GLEC}, Section 3 Module 2, Europe and South America road, {label}; {ROAD_SFD}", "sfd")

# GLEC p.106: Asia (except China and India) and Africa: +13% for vans, +22% for heavier vehicles,
# applied to the Europe and South America values.
for vid, label, wtt, ttw in EU_SA:
    up = 1.13 if vid.startswith("van_") else 1.22
    leg("road", "glec/road/asia_africa/" + vid, wtt * up, ttw * up,
        f"{GLEC}, Section 3 Module 2, road, Asia (except China and India) and Africa: the Europe and South America value "
        f"({label}) with GLEC's +{round((up - 1) * 100)}% uplift for {'vans' if up == 1.13 else 'vehicles over 3.5 t GVW'} (p. 106); {ROAD_SFD}",
        "sfd")

# Table 6, North America (SmartWay 2025).
NA = [
    ("van", "Van (<3.5 t)", 153, 756), ("auto_carrier", "Auto Carrier", 19, 99), ("dray", "Dray", 15, 78),
    ("expedited", "Expedited", 135, 701), ("flatbed", "Flatbed", 14, 75), ("heavy_bulk", "Heavy Bulk", 13, 65),
    ("ltl_dry_van", "LTL/Dry Van", 38, 195), ("mixed_general", "Mixed/General", 16, 80), ("moving", "Moving", 66, 343),
    ("package", "Package", 203, 1049), ("refrigerated", "Refrigerated", 15, 80), ("specialized", "Specialized", 21, 110),
    ("tanker", "Tanker", 12, 60), ("tl_dry_van", "TL/Dry Van", 15, 77),
]
for vid, label, wtt, ttw in NA:
    leg("road", "glec/road/na/" + vid, wtt, ttw,
        f"{GLEC}, Section 3 Module 2, Table 6, North American road (SmartWay 2025): {label}; {ROAD_SFD}", "sfd")

# Table 12, China (all average load, 93% load factor, 19.5% empty running).
CN = [
    ("rigid_3_5_4_5t/diesel", "Rigid truck LDT 3.5-4.5 t GVW, diesel", 109.61, 360.83),
    ("rigid_4_5_5_5t/diesel", "Rigid truck MDT 4.5-5.5 t GVW, diesel", 97.15, 319.79),
    ("rigid_5_5_7t/diesel", "Rigid truck MDV 5.5-7.0 t GVW, diesel", 93.37, 307.37),
    ("rigid_7_8_5t/diesel", "Rigid truck MDV 7.0-8.5 t GVW, diesel", 72.33, 238.08),
    ("rigid_8_5_10_5t/diesel", "Rigid truck MDV 8.5-10.5 t GVW, diesel", 60.88, 200.39),
    ("rigid_10_5_12_5t/diesel", "Rigid truck MDV 10.5-12.5 t GVW, diesel", 53.99, 177.72),
    ("rigid_12_5_16t/diesel", "Rigid truck HDV 12.5-16.0 t GVW, diesel", 49.39, 162.57),
    ("rigid_16_20t/diesel", "Rigid truck HDV 16.0-20.0 t GVW, diesel", 35.5, 116.85),
    ("rigid_20_25t/diesel", "Rigid truck HDV 20.0-25.0 t GVW, diesel", 24.85, 81.81),
    ("rigid_25_31t/diesel", "Rigid truck HDV 25.0-31.0 t GVW, diesel", 21.25, 69.96),
    ("rigid_31t/diesel", "Rigid truck HDV > 31.0 t GVW, diesel", 22.24, 73.2),
    ("artic_18t/diesel", "Articulated truck HDV up to 18.0 t GVW, diesel", 36.7, 120.81),
    ("artic_18_27t/diesel", "Articulated truck HDV 18.0-27.0 t GVW, diesel", 24.99, 82.26),
    ("artic_27_35t/diesel", "Articulated truck HDV 27.0-35.0 t GVW, diesel", 22.54, 74.21),
    ("artic_35_40t/diesel", "Articulated truck HDV 35.0-40.0 t GVW, diesel", 18.84, 62.02),
    ("artic_40_43t/diesel", "Articulated truck HDV 40.0-43.0 t GVW, diesel", 18.22, 59.96),
    ("artic_43_46t/diesel", "Articulated truck HDV 43.0-46.0 t GVW, diesel", 17.49, 57.57),
    ("artic_46_49t/diesel", "Articulated truck HDV 46.0-49.0 t GVW, diesel", 17.34, 57.09),
    ("artic_49t/diesel", "Articulated truck HDV above 49.0 t GVW, diesel", 17.22, 56.67),
    ("dump_3_5_4_5t/diesel", "Dump truck LDT 3.5-4.5 t GVW, diesel", 130.16, 428.46),
    ("dump_4_5_5_5t/diesel", "Dump truck MDT 4.5-5.5 t GVW, diesel", 96.73, 318.43),
    ("dump_5_5_7t/diesel", "Dump truck MDV 5.5-7.0 t GVW, diesel", 91.62, 301.6),
    ("dump_7_8_5t/diesel", "Dump truck MDV 7.0-8.5 t GVW, diesel", 71.07, 233.96),
    ("dump_8_5_10_5t/diesel", "Dump truck MDV 8.5-10.5 t GVW, diesel", 61.12, 201.19),
    ("dump_10_5_12_5t/diesel", "Dump truck MDV 10.5-12.5 t GVW, diesel", 52.39, 172.48),
    ("dump_12_5_16t/diesel", "Dump truck HDV 12.5-16.0 t GVW, diesel", 48.04, 158.14),
    ("dump_16_20t/diesel", "Dump truck HDV 16.0-20.0 t GVW, diesel", 35.6, 117.21),
    ("dump_20_25t/diesel", "Dump truck HDV 20.0-25.0 t GVW, diesel", 23.28, 76.63),
    ("dump_25_31t/diesel", "Dump truck HDV 25.0-31.0 t GVW, diesel", 21.47, 70.68),
    ("dump_31t/diesel", "Dump truck HDV above 31.0 t GVW, diesel", 18.62, 61.3),
    ("artic_14_24t/lng", "Articulated truck 14-24 t GVW, LNG", 74.95, 175.72),
    ("artic_24_25t/lng", "Articulated truck 24-25 t GVW, LNG", 35.84, 84.02),
    ("artic_25_29t/lng", "Articulated truck 25-29 t GVW, LNG", 29.84, 69.95),
    ("artic_29_31t/lng", "Articulated truck 29-31 t GVW, LNG", 24.66, 57.83),
    ("artic_31_60t/lng", "Articulated truck 31-60 t GVW, LNG", 19.87, 46.58),
    ("dump_14_24t/lng", "Dump truck 14-24 t GVW, LNG", 57.89, 135.72),
    ("dump_24_25t/lng", "Dump truck 24-25 t GVW, LNG", 28.63, 67.12),
    ("dump_25_29t/lng", "Dump truck 25-29 t GVW, LNG", 24.69, 57.88),
    ("dump_29_31t/lng", "Dump truck 29-31 t GVW, LNG", 20.38, 47.79),
    ("dump_31_60t/lng", "Dump truck 31-60 t GVW, LNG", 16.4, 38.44),
    ("rigid_14_24t/lng", "Rigid truck 14-24 t GVW, LNG", 58.28, 136.64),
    ("rigid_24_25t/lng", "Rigid truck 24-25 t GVW, LNG", 28.63, 67.12),
    ("rigid_25_29t/lng", "Rigid truck 25-29 t GVW, LNG", 24.14, 56.59),
    ("rigid_29_31t/lng", "Rigid truck 29-31 t GVW, LNG", 20.49, 48.04),
    ("rigid_31_60t/lng", "Rigid truck 31-60 t GVW, LNG", 16.52, 38.74),
    ("truck_4_5t/electricity", "Truck LDV up to 4.5 t GVW, electricity", 135.44, 0),
    ("truck_4_5_12t/electricity", "Truck MDV 4.5-12.0 t GVW, electricity", 74.87, 0),
    ("truck_12t/electricity", "Truck HDV above 12 t GVW, electricity", 145.25, 0),
    ("truck_4_5t/hydrogen", "Truck LDV up to 4.5 t GVW, hydrogen", 328.1, 0),
    ("truck_4_5_12t/hydrogen", "Truck MDV 4.5-12.0 t GVW, hydrogen", 173.1, 0),
    ("truck_12t/hydrogen", "Truck HDV above 12 t GVW, hydrogen", 198.86, 0),
]
for vid, label, wtt, ttw in CN:
    leg("road", "glec/road/cn/" + vid, wtt, ttw,
        f"{GLEC}, Section 3 Module 2, Table 12, China road: {label} (load 93%, empty running 19.5%); {ROAD_SFD}", "sfd")

# Tables 13 and 13a, India.
IN = [
    ("small_cv/diesel", "Table 13: small commercial vehicle, GVW < 3.5 t, diesel", 69.2, 229.8),
    ("small_cv/petrol", "Table 13: small commercial vehicle, GVW < 3.5 t, petrol", 93.1, 291.1),
    ("small_cv/cng", "Table 13: small commercial vehicle, GVW < 3.5 t, CNG", 116.6, 312.3),
    ("medium_cv_1/diesel", "Table 13: medium commercial vehicle 1, GVW 3-5 t, diesel", 44.3, 147.1),
    ("medium_cv_1/cng", "Table 13: medium commercial vehicle 1, GVW 3-5 t, CNG", 67.1, 179.8),
    ("medium_cv_2/diesel", "Table 13: medium commercial vehicle 2, GVW 5-12 t, diesel", 25.8, 85.8),
    ("medium_cv_2/cng", "Table 13: medium commercial vehicle 2, GVW 5-12 t, CNG", 43.9, 117.7),
    ("heavy_cv_1/diesel", "Table 13: heavy commercial vehicle 1, GVW 12-20 t, diesel", 20.9, 69.4),
    ("heavy_cv_1/cng", "Table 13: heavy commercial vehicle 1, GVW 12-20 t, CNG", 20.1, 53.9),
    ("heavy_cv_2/diesel", "Table 13: heavy commercial vehicle 2, GVW 20-30 t, diesel", 17.0, 56.5),
    ("heavy_cv_3/diesel", "Table 13: heavy commercial vehicle 3, GVW 30-50 t, diesel", 13.3, 44.1),
    ("tractor_trailer/diesel", "Table 13: tractor trailer, GVW 30-60 t, diesel", 12.3, 40.7),
    ("rigid_3_5t/electricity", "Table 13a: rigid truck < 3.5 t GVW, electricity", 138.3, 0),
    ("rigid_3_5_12t/electricity", "Table 13a: rigid truck 3.5-12 t GVW, electricity", 133.9, 0),
    ("rigid_14t/electricity", "Table 13a: rigid truck 14 t GVW, electricity", 128.9, 0),
    ("rigid_18_5t/electricity", "Table 13a: rigid truck 18.5 t GVW, electricity", 77.1, 0),
    ("artic_55t/electricity", "Table 13a: articulated truck 55 t GVW, electricity", 38.7, 0),
]
for vid, label, wtt, ttw in IN:
    leg("road", "glec/road/in/" + vid, wtt, ttw,
        f"{GLEC}, Section 3 Module 2, Indian road, {label}; {ROAD_SFD}", "sfd")


def find(vt):
    for l in legs:
        if l["vehicle_type"] == vt:
            return l
    raise KeyError(vt)


# Temperature-controlled road freight (GLEC p.106): +15% for vans, +12% for heavier vehicles, on the
# regional values for Europe, South America, Asia and Africa. North America has its own Refrigerated row.
REEFER = [
    ("glec/road/eu_sa/rigid_26_32t/diesel", "glec/road/eu_sa/temperature_controlled/rigid_26_32t/diesel", 1.12),
    ("glec/road/eu_sa/artic_34_40t/diesel", "glec/road/eu_sa/temperature_controlled/artic_34_40t/diesel", 1.12),
    ("glec/road/eu_sa/van_3_5t/diesel", "glec/road/eu_sa/temperature_controlled/van_3_5t/diesel", 1.15),
    ("glec/road/asia_africa/rigid_26_32t/diesel", "glec/road/asia_africa/temperature_controlled/rigid_26_32t/diesel", 1.12),
    ("glec/road/asia_africa/artic_34_40t/diesel", "glec/road/asia_africa/temperature_controlled/artic_34_40t/diesel", 1.12),
    ("glec/road/asia_africa/van_3_5t/diesel", "glec/road/asia_africa/temperature_controlled/van_3_5t/diesel", 1.15),
    ("glec/road/cn/artic_14_24t/lng", "glec/road/cn/temperature_controlled/artic_14_24t/lng", 1.12),
    ("glec/road/in/heavy_cv_1/diesel", "glec/road/in/temperature_controlled/heavy_cv_1/diesel", 1.12),
]
for base, vid, up in REEFER:
    b = find(base)
    leg("road", vid, b["wtt_g_per_tkm"] * up, b["ttw_g_per_tkm"] * up,
        f"{GLEC}, Section 3 Module 2, road, temperature-controlled: {base} with GLEC's +{round((up - 1) * 100)}% uplift "
        f"for temperature-controlled {'vans' if up == 1.15 else 'vehicles over 3.5 t GVW'} (p. 106; given for Europe, South "
        f"America, Asia and Africa); {ROAD_SFD}", "sfd")

# ---------------------------------------------------------------- rail
RAIL = f"{GLEC}, Section 3 Module 2, rail"
RAIL_TYPES = [
    ("average_mixed", "Average/mixed (load 60%, empty 33%)", 7.1, 23.6, 10.8),
    ("container", "Container (load 50%, empty 17%)", 6.6, 21.9, 10.1),
    ("cars", "Cars (load 85%, empty 33%)", 15.3, 50.7, 23.3),
    ("chemicals", "Chemicals (load 100%, empty 50%)", 6.1, 20.3, 9.4),
    ("coal_steel", "Coal & steel (load 100%, empty 50%)", 4.7, 15.7, 7.3),
    ("building_materials", "Building materials (load 100%, empty 50%)", 5.9, 19.7, 9.1),
    ("manufactured_products", "Manufactured products (load 75%, empty 38%)", 6.2, 20.7, 9.5),
    ("cereals", "Cereals (load 100%, empty 38%)", 4.7, 15.5, 7.2),
    ("truck_trailer_on_train", "Truck + trailer on train (load 85%, empty 33%)", 13.9, 49.0, 14.6),
    ("trailer_on_train", "Trailer only on train (load 85%, empty 33%)", 8.7, 30.8, 9.3),
]
for vid, label, wtt, ttw, el in RAIL_TYPES:
    leg("rail", "glec/rail/eu/diesel/" + vid, wtt, ttw, f"{RAIL}, Table 4, European diesel traction: {label}", "")
    leg("rail", "glec/rail/eu/electric/" + vid, el, 0,
        f"{RAIL}, Table 5, European electric traction at the average 2025 EU electricity mix: {label}; "
        f"all of it is electricity generation and distribution (well-to-tank)", "")
# EU average where the traction is unknown: 18.4 g WTW (p.98), which GLEC derives from 62% of EU track being
# electrified (UIC 2017). GLEC gives no TTW/WTT split; this split weights the Table 4 and Table 5 average/mixed rows
# 38% / 62% the same way (8.97 + 9.39 = 18.36 g).
leg("rail", "glec/rail/eu/average_traction_unknown", 0.38 * 7.1 + 0.62 * 10.8, 0.38 * 23.6,
    f"{RAIL}, Europe: EU average where the traction is unknown, 18.4 g CO2e/t-km WTW (p. 98, based on 62% of EU track "
    f"being electrified); the TTW/WTT split weights GLEC's Table 4 diesel and Table 5 electric average/mixed values 38% / 62%", "")
leg("rail", "glec/rail/na/average", 2.7, 13.4, f"{RAIL}, North America: US average (diesel), 16.1 g CO2e/t-km WTW (p. 98)", "")
leg("rail", "glec/rail/in/average", 9.6, 1.5,
    f"{RAIL}, India: Indian average, diesel and electric traction mixed, 11.1 g CO2e/t-km WTW (p. 98)", "")

# ---------------------------------------------------------------- air (Table 1, including the +95 km DAF: per GCD)
AIR = [
    ("freighter/short_haul", "freighter, short-haul (< 1500 km)", 260, 1252),
    ("freighter/long_haul", "freighter, long-haul (> 1500 km)", 110, 528),
    ("belly/short_haul", "belly freight, short-haul (< 1500 km)", 229, 1103),
    ("belly/long_haul", "belly freight, long-haul (> 1500 km)", 160, 770),
    ("unknown/short_haul", "aircraft type unknown, short-haul (< 1500 km)", 243, 1170),
    ("unknown/long_haul", "aircraft type unknown, long-haul (> 1500 km)", 138, 661),
]
for vid, label, wtt, ttw in AIR:
    leg("air", "glec/air/" + vid, wtt, ttw,
        f"{GLEC}, Section 3 Module 2, Table 1, air: {label}, including the +95 km DAF, for use with the great circle distance", "gcd")
leg("air", "glec/air/in/domestic", 216, 1043,
    f"{GLEC}, Section 3 Module 2, Indian domestic air (aircraft type unknown), for use with the great circle distance (p. 95)", "gcd")

# ---------------------------------------------------------------- inland waterway (Table 2)
IWW = [
    ("motor_vessel_50m", "motor vessel < 50 m (< 650 t)", 17.8, 59.2),
    ("motor_vessel_50_80m", "motor vessel 50-80 m (650-1000 t), load 55%", 7.9, 26.1),
    ("motor_vessel_85_110m", "motor vessel 85-110 m (1000-2000 t), load 52%", 4.9, 16.4),
    ("motor_vessel_135m", "motor vessel 135 m (2000-3000 t), load 50%", 5.0, 16.7),
    ("coupled_convoy", "coupled convoy (163-185 m), load 61%", 4.6, 15.1),
    ("pushed_convoy_2_barges", "pushed convoy, push boat + 2 barges, load 70% (US operations)", 4.7, 15.5),
    ("pushed_convoy_4_5_barges", "pushed convoy, push boat + 4/5 barges, load 70% (US operations)", 2.6, 8.7),
    ("pushed_convoy_6_barges", "pushed convoy, push boat + 6 barges, load 70% (US operations)", 1.9, 6.4),
    ("tanker", "tanker vessel, load 65%", 5.7, 19.0),
    ("container_110m", "container vessel 110 m, load 75%", 6.8, 22.5),
    ("container_135m", "container vessel 135 m, load 75%", 5.2, 17.4),
    ("container_coupled_convoy", "container coupled convoy, load 68%", 5.2, 17.4),
]
for vid, label, wtt, ttw in IWW:
    leg("inland_waterway", "glec/iww/" + vid, wtt, ttw,
        f"{GLEC}, Section 3 Module 2, Table 2, inland waterways (mainly European data): {label}, diesel", "sfd")

# ---------------------------------------------------------------- sea, non-container (Tables 14-17, IMO 4th GHG study)
# (type id, label, unit, size, {fuel: (wtt, ttw)}) per actual distance; GLEC's "with 15% DAF" column is per SFD.
SEA = [
    ("bulk_carrier", "bulk carrier", "dwt", "0-9999", {"hfo": (4.7, 26.5), "vlsfo": (5.6, 26.5), "mdo": (4.8, 25.7)}),
    ("bulk_carrier", "bulk carrier", "dwt", "10000-34999", {"hfo": (1.3, 7.3), "vlsfo": (1.5, 7.3), "mdo": (1.3, 7.0)}),
    ("bulk_carrier", "bulk carrier", "dwt", "35000-59999", {"hfo": (0.9, 5.3), "vlsfo": (1.1, 5.3), "mdo": (1.0, 5.2)}),
    ("bulk_carrier", "bulk carrier", "dwt", "60000-99999", {"hfo": (0.8, 4.4), "vlsfo": (0.9, 4.4), "mdo": (0.8, 4.2)}),
    ("bulk_carrier", "bulk carrier", "dwt", "100000-199999", {"hfo": (0.5, 3.0), "vlsfo": (0.6, 3.0), "mdo": (0.5, 2.9)}),
    ("bulk_carrier", "bulk carrier", "dwt", "200000+", {"hfo": (0.5, 2.7), "vlsfo": (0.6, 2.7), "mdo": (0.5, 2.6)}),
    ("chemical_tanker", "chemical tanker", "dwt", "0-4999", {"hfo": (9.1, 51.2), "vlsfo": (10.9, 51.2), "mdo": (9.3, 49.6)}),
    ("chemical_tanker", "chemical tanker", "dwt", "5000-9999", {"hfo": (4.0, 22.7), "vlsfo": (4.8, 22.7), "mdo": (4.1, 22.0)}),
    ("chemical_tanker", "chemical tanker", "dwt", "10000-19999", {"hfo": (2.8, 15.4), "vlsfo": (3.3, 15.4), "mdo": (2.8, 15.0)}),
    ("chemical_tanker", "chemical tanker", "dwt", "20000-39999", {"hfo": (1.7, 9.4), "vlsfo": (2.0, 9.4), "mdo": (1.7, 9.2)}),
    ("chemical_tanker", "chemical tanker", "dwt", "40000+", {"hfo": (1.3, 7.2), "vlsfo": (1.5, 7.2), "mdo": (1.3, 7.0)}),
    ("general_cargo", "general cargo", "dwt", "0-4999", {"hfo": (4.0, 22.4), "vlsfo": (4.8, 22.4), "mdo": (4.1, 21.8)}),
    ("general_cargo", "general cargo", "dwt", "5000-9999", {"hfo": (3.2, 18.1), "vlsfo": (3.9, 18.1), "mdo": (3.3, 17.5)}),
    ("general_cargo", "general cargo", "dwt", "10000-19999", {"hfo": (2.9, 16.1), "vlsfo": (3.4, 16.1), "mdo": (2.9, 15.6)}),
    ("general_cargo", "general cargo", "dwt", "20000+", {"hfo": (1.5, 8.3), "vlsfo": (1.8, 8.3), "mdo": (1.5, 8.1)}),
    ("liquefied_gas_tanker", "liquefied gas tanker", "cbm", "0-49999", {"hfo": (7.2, 40.2), "vlsfo": (8.6, 40.2), "mdo": (7.3, 39.0)}),
    ("liquefied_gas_tanker", "liquefied gas tanker", "cbm", "50000-99999", {"hfo": (2.1, 11.7), "vlsfo": (2.5, 11.7), "mdo": (2.1, 11.3)}),
    ("liquefied_gas_tanker", "liquefied gas tanker", "cbm", "100000-199999", {"hfo": (1.6, 9.2), "vlsfo": (2.0, 9.2), "mdo": (1.7, 8.9)}),
    ("liquefied_gas_tanker", "liquefied gas tanker", "cbm", "200000+", {"hfo": (1.7, 9.7), "vlsfo": (2.1, 9.7), "mdo": (1.8, 9.4)}),
    ("oil_tanker", "oil tanker", "dwt", "0-4999", {"hfo": (13.0, 73.0), "vlsfo": (15.6, 73.0), "mdo": (13.3, 70.8)}),
    ("oil_tanker", "oil tanker", "dwt", "5000-9999", {"hfo": (7.2, 40.1), "vlsfo": (8.5, 40.1), "mdo": (7.3, 38.8)}),
    ("oil_tanker", "oil tanker", "dwt", "10000-19999", {"hfo": (5.6, 31.4), "vlsfo": (6.7, 31.4), "mdo": (5.7, 30.4)}),
    ("oil_tanker", "oil tanker", "dwt", "20000-59999", {"hfo": (2.7, 15.3), "vlsfo": (3.3, 15.3), "mdo": (2.8, 14.9)}),
    ("oil_tanker", "oil tanker", "dwt", "60000-79999", {"hfo": (1.6, 8.8), "vlsfo": (1.9, 8.8), "mdo": (1.6, 8.5)}),
    ("oil_tanker", "oil tanker", "dwt", "80000-119999", {"hfo": (1.2, 6.9), "vlsfo": (1.5, 6.9), "mdo": (1.3, 6.7)}),
    ("oil_tanker", "oil tanker", "dwt", "120000-199999", {"hfo": (0.9, 5.1), "vlsfo": (1.1, 5.1), "mdo": (0.9, 4.9)}),
    ("oil_tanker", "oil tanker", "dwt", "200000+", {"hfo": (0.6, 3.1), "vlsfo": (0.7, 3.1), "mdo": (0.6, 3.0)}),
    ("other_liquids_tanker", "other liquids tanker", "dwt", "0-999", {"hfo": (185.4, 1038.3), "vlsfo": (221.4, 1038.3), "mdo": (188.4, 1006.8)}),
    ("other_liquids_tanker", "other liquids tanker", "dwt", "1000+", {"hfo": (5.0, 27.8), "vlsfo": (5.9, 27.8), "mdo": (5.1, 27.0)}),
    ("ferry_ropax", "ferry-RoPax", "gt", "0-1999", {"hfo": (80.9, 453.0), "vlsfo": (96.6, 453.0), "mdo": (82.2, 439.2)}),
    ("ferry_ropax", "ferry-RoPax", "gt", "2000-4999", {"hfo": (39.1, 218.8), "vlsfo": (46.7, 218.8), "mdo": (39.7, 212.2)}),
    ("ferry_ropax", "ferry-RoPax", "gt", "5000-9999", {"hfo": (30.3, 169.5), "vlsfo": (36.1, 169.5), "mdo": (30.8, 164.3)}),
    ("ferry_ropax", "ferry-RoPax", "gt", "10000-19999", {"hfo": (19.3, 108.3), "vlsfo": (23.1, 108.3), "mdo": (19.7, 105.1)}),
    ("ferry_ropax", "ferry-RoPax", "gt", "20000+", {"hfo": (14.6, 81.8), "vlsfo": (17.5, 81.8), "mdo": (14.8, 79.3)}),
    ("refrigerated_bulk", "refrigerated bulk", "dwt", "0-1999", {"hfo": (24.0, 134.4), "vlsfo": (28.7, 134.4), "mdo": (24.4, 130.4)}),
    ("refrigerated_bulk", "refrigerated bulk", "dwt", "2000-5999", {"hfo": (11.6, 65.1), "vlsfo": (13.9, 65.1), "mdo": (11.8, 63.2)}),
    ("refrigerated_bulk", "refrigerated bulk", "dwt", "6000-9999", {"hfo": (8.7, 48.8), "vlsfo": (10.4, 48.8), "mdo": (8.8, 47.3)}),
    ("refrigerated_bulk", "refrigerated bulk", "dwt", "10000+", {"hfo": (6.4, 35.9), "vlsfo": (7.7, 35.9), "mdo": (6.5, 34.8)}),
    ("roro", "Ro-Ro (t-km of gross load: truck and cargo)", "dwt", "0-4999", {"hfo": (30.7, 172.2), "vlsfo": (36.7, 172.2), "mdo": (31.2, 167.0)}),
    ("roro", "Ro-Ro (t-km of gross load: truck and cargo)", "dwt", "5000-9999", {"hfo": (6.7, 37.7), "vlsfo": (8.0, 37.7), "mdo": (6.8, 36.5)}),
    ("roro", "Ro-Ro (t-km of gross load: truck and cargo)", "dwt", "10000-14999", {"hfo": (5.6, 31.3), "vlsfo": (6.7, 31.3), "mdo": (5.7, 30.4)}),
    ("roro", "Ro-Ro (t-km of gross load: truck and cargo)", "dwt", "15000+", {"hfo": (2.9, 16.5), "vlsfo": (3.5, 16.5), "mdo": (3.0, 16.0)}),
    ("vehicle_carrier", "vehicle carrier", "gt", "0-29999", {"hfo": (14.9, 83.7), "vlsfo": (17.9, 83.7), "mdo": (15.2, 81.2)}),
    ("vehicle_carrier", "vehicle carrier", "gt", "30000-49999", {"hfo": (7.1, 39.9), "vlsfo": (8.5, 39.9), "mdo": (7.2, 38.7)}),
    ("vehicle_carrier", "vehicle carrier", "gt", "50000+", {"hfo": (5.8, 32.5), "vlsfo": (6.9, 32.5), "mdo": (5.9, 31.5)}),
]
FUEL_NAMES = {"hfo": "HFO", "vlsfo": "VLSFO", "mdo": "MDO"}
for tid, label, unit, size, fuels in SEA:
    for fuel, (wtt, ttw) in fuels.items():
        sid = size.replace("+", "_plus")
        leg("sea", f"glec/sea/{tid}/{sid}_{unit}/{fuel}", wtt, ttw,
            f"{GLEC}, Section 3 Module 2, Tables 14-17, sea, non-container vessels (IMO Fourth GHG Study): {label}, "
            f"{size} {unit.upper()}, {FUEL_NAMES[fuel]}; per actual distance (GLEC's sea DAF of +15% turns the shortest "
            f"feasible distance into it)", "actual")

# ---------------------------------------------------------------- sea, container (Tables 18-19, Clean Cargo 2025)
TEU_T = 10.0  # GLEC: an average weight of 10 tonnes per TEU when the cargo mass is not known
LANES = {
    "industry_average": ("industry average (origin-destination pair unknown)", (13.0, 60.4), (26.8, 124.7)),
    "panama_trade": ("aggregated lanes: Panama trade", (17.5, 81.3), (32.2, 149.8)),
    "trans_atlantic": ("aggregated lanes: Trans-Atlantic", (17.5, 81.3), (33.3, 154.7)),
    "trans_suez": ("aggregated lanes: Trans-Suez", (8.7, 40.4), (20.4, 95.0)),
    "trans_pacific": ("aggregated lanes: Trans-Pacific", (11.3, 52.3), (23.8, 110.8)),
    "other_global": ("aggregated lanes: other global", (15.9, 74.1), (31.2, 145.1)),
    "asia_africa": ("Asia to-from Africa", (15.9, 74.0), (30.6, 142.2)),
    "asia_med_bs": ("Asia to-from Mediterranean/Black Sea", (9.1, 42.3), (21.4, 99.5)),
    "asia_me_india": ("Asia to-from Middle East/India", (14.6, 68.0), (29.5, 137.3)),
    "asia_na_ec": ("Asia to-from North America EC/Gulf", (10.9, 50.6), (24.5, 113.9)),
    "asia_na_wc": ("Asia to-from North America WC", (10.9, 50.8), (23.1, 107.2)),
    "asia_north_europe": ("Asia to-from North Europe", (7.6, 35.3), (18.8, 87.2)),
    "asia_oceania": ("Asia to-from North Oceania", (17.8, 82.7), (32.0, 148.9)),
    "asia_south_america": ("Asia to-from South America (incl. Central America)", (12.4, 57.4), (26.1, 121.3)),
    "europe_africa": ("Europe (North & Med) to-from Africa", (22.0, 102.3), (41.6, 193.3)),
    "europe_south_america": ("Europe (North & Med) to-from South America (incl. Central America)", (17.3, 80.4), (33.5, 155.8)),
    "europe_me_india": ("Europe (North & Med) to-from Middle East/India", (12.3, 57.2), (25.3, 117.8)),
    "europe_oceania": ("Europe (North & Med) to-from Oceania (via Suez/via Panama)", (22.2, 103.2), (37.2, 173.0)),
    "med_bs_na_ec": ("Mediterranean/Black Sea to-from North America EC/Gulf", (18.8, 87.2), (35.6, 165.3)),
    "med_bs_na_wc": ("Mediterranean/Black Sea to-from North America WC", (14.0, 65.2), (28.7, 133.3)),
    "na_africa": ("North America EC/Gulf/WC to-from Africa", (23.9, 111.0), (43.6, 202.7)),
    "na_oceania": ("North America EC/Gulf/WC to-from Oceania", (13.1, 60.8), (25.6, 119.0)),
    "na_south_america": ("North America EC/Gulf/WC to-from South America (incl. Central America)", (21.0, 97.4), (38.8, 180.4)),
    "na_me_india": ("North America EC/Gulf/WC to-from Middle East/India", (15.8, 73.5), (26.9, 124.9)),
    "north_europe_na_ec": ("North Europe to-from North America EC/Gulf", (18.0, 83.9), (33.4, 155.4)),
    "north_europe_na_wc": ("North Europe to-from North America WC", (22.5, 104.5), (39.7, 184.3)),
    "south_america_africa": ("South America (incl. Central America) to-from Africa", (42.9, 199.3), (69.5, 323.2)),
    "intra_africa": ("Intra Africa", (36.0, 167.2), (69.1, 321.3)),
    "intra_na": ("Intra North America EC/Gulf/WC", (56.9, 264.5), (77.4, 359.5)),
    "intra_south_america": ("Intra South America (incl. Central America)", (29.0, 135.0), (52.8, 245.1)),
    "se_asia_ne_asia": ("SE Asia to-from NE Asia", (19.6, 90.9), (36.1, 167.5)),
    "intra_ne_asia": ("Intra NE Asia", (24.5, 114.0), (45.6, 211.7)),
    "intra_se_asia": ("Intra SE Asia", (22.9, 106.4), (40.6, 188.8)),
    "north_europe_med_bs": ("North Europe to-from Mediterranean/Black Sea", (20.6, 95.6), (39.1, 181.8)),
    "intra_med_bs": ("Intra Mediterranean/Black Sea", (35.6, 165.5), (68.9, 320.0)),
    "intra_north_europe": ("Intra North Europe", (30.9, 143.8), (54.4, 252.7)),
    "intra_me_india": ("Intra Middle East/India", (26.7, 124.3), (48.5, 225.5)),
    "other": ("Other (lanes not listed)", (15.5, 71.9), (32.1, 149.3)),
}
for lid, (label, dry, reefer) in LANES.items():
    for kind, (wtt, ttw) in (("dry", dry), ("reefer", reefer)):
        leg("sea", f"glec/sea/container/{lid}/{kind}", wtt / TEU_T, ttw / TEU_T,
            f"{GLEC}, Section 3 Module 2, Tables 18-19, container vessels (Clean Cargo trade lane factors, reporting year 2025): "
            f"{label}, {kind}, {wtt:g} + {ttw:g} g CO2e/TEU-km (WTT + TTW) / 10 t per TEU (GLEC's average when the cargo mass is "
            f"not known); includes GLEC's +15% distance conversion, so it is per shortest feasible distance", "sfd")

# ---------------------------------------------------------------- regions
continent = {}
with open(os.path.join(HERE, "countries.csv"), newline="", encoding="utf-8") as f:
    for row in csv.DictReader(f):
        continent[row["code"]] = row["continent"]


def countries(*conts, minus=()):
    return sorted(c for c, k in continent.items() if k in conts and c not in minus)


LAND_NOTE = ("GLEC's road regions (Section 3 Module 2): North America (SmartWay, US and Canada), China, India, "
             "Asia (except China and India) and Africa, Europe and South America (with Mexico, Central America and the Caribbean). "
             "Continents from OurAirports.")
na_land = ["CA", "US"]
regions = [
    {"name": "na", "label": "North America", "set": "land", "countries": na_land},
    {"name": "cn", "label": "China", "set": "land", "countries": ["CN"]},
    {"name": "in", "label": "India", "set": "land", "countries": ["IN"]},
    {"name": "asia_africa", "label": "Asia (except China and India) and Africa", "set": "land",
     "countries": countries("AS", "AF", minus=("CN", "IN"))},
    {"name": "eu_sa", "label": "Europe and South America", "set": "land",
     "countries": countries("EU", "SA") + countries("NA", minus=tuple(na_land))},
]
# Sea zones for GLEC's container trade lanes. First match wins.
ne_asia = ["CN", "HK", "JP", "KP", "KR", "MN", "MO", "TW"]
se_asia = ["BN", "ID", "KH", "LA", "MM", "MY", "PH", "SG", "TH", "TL", "VN"]
me_india = ["AE", "BD", "BH", "IN", "IQ", "IR", "JO", "KW", "LK", "MV", "OM", "PK", "QA", "SA", "YE"]
med_bs = ["AL", "BA", "BG", "CY", "GE", "GI", "GR", "HR", "IL", "IT", "LB", "MC", "MD", "ME", "MT", "PS", "RO", "SI",
          "SM", "SY", "TR", "UA", "VA"]
regions += [
    {"name": "ne_asia", "label": "NE Asia", "set": "sea", "countries": ["RU"], "lon_min": 100},
    {"name": "ne_asia", "label": "NE Asia", "set": "sea", "countries": ne_asia},
    {"name": "se_asia", "label": "SE Asia", "set": "sea", "countries": se_asia},
    {"name": "me_india", "label": "Middle East/India", "set": "sea", "countries": me_india},
    {"name": "med_bs", "label": "Mediterranean/Black Sea", "set": "sea", "countries": ["RU"], "lat_max": 48},
    {"name": "med_bs", "label": "Mediterranean/Black Sea", "set": "sea", "countries": ["ES", "FR"], "lon_min": 3},
    {"name": "med_bs", "label": "Mediterranean/Black Sea", "set": "sea", "countries": ["ES"], "lon_min": -5.6, "lat_max": 42.5},
    {"name": "med_bs", "label": "Mediterranean/Black Sea", "set": "sea", "countries": med_bs},
    {"name": "north_europe", "label": "North Europe", "set": "sea", "countries": countries("EU", minus=tuple(med_bs))},
    {"name": "africa", "label": "Africa", "set": "sea", "countries": countries("AF")},
    {"name": "na_wc", "label": "North America WC", "set": "sea", "countries": na_land, "lon_max": -100},
    {"name": "na_ec", "label": "North America EC/Gulf", "set": "sea", "countries": na_land},
    {"name": "south_america", "label": "South America (incl. Central America)", "set": "sea",
     "countries": countries("SA") + countries("NA", minus=tuple(na_land + ["GL"]))},
    {"name": "oceania", "label": "Oceania", "set": "sea", "countries": countries("OC")},
]
SEA_NOTE = ("Sea zones for GLEC's container trade lanes (Clean Cargo): country lists by Sootcase; Spain, France and Russia "
            "split by coordinates (Mediterranean: Spain east of 5.6 W and south of 42.5 N, Spain or France east of 3 E, "
            "Russia south of 48 N; NE Asia: Russia east of 100 E), US and Canada west of 100 W = West Coast.")

ASIA, EUROPE, NA_Z = ["ne_asia", "se_asia"], ["north_europe", "med_bs"], ["na_ec", "na_wc"]
lane_rules = [  # (a zones, b zones, lane id); either direction; first match wins
    (["ne_asia"], ["ne_asia"], "intra_ne_asia"), (["se_asia"], ["se_asia"], "intra_se_asia"),
    (["se_asia"], ["ne_asia"], "se_asia_ne_asia"),
    (ASIA, ["africa"], "asia_africa"), (ASIA, ["med_bs"], "asia_med_bs"), (ASIA, ["me_india"], "asia_me_india"),
    (ASIA, ["na_ec"], "asia_na_ec"), (ASIA, ["na_wc"], "asia_na_wc"), (ASIA, ["north_europe"], "asia_north_europe"),
    (ASIA, ["oceania"], "asia_oceania"), (ASIA, ["south_america"], "asia_south_america"),
    (["north_europe"], ["north_europe"], "intra_north_europe"), (["med_bs"], ["med_bs"], "intra_med_bs"),
    (["north_europe"], ["med_bs"], "north_europe_med_bs"),
    (EUROPE, ["africa"], "europe_africa"), (EUROPE, ["south_america"], "europe_south_america"),
    (EUROPE, ["me_india"], "europe_me_india"), (EUROPE, ["oceania"], "europe_oceania"),
    (["med_bs"], ["na_ec"], "med_bs_na_ec"), (["med_bs"], ["na_wc"], "med_bs_na_wc"),
    (["north_europe"], ["na_ec"], "north_europe_na_ec"), (["north_europe"], ["na_wc"], "north_europe_na_wc"),
    (NA_Z, NA_Z, "intra_na"),
    (NA_Z, ["africa"], "na_africa"), (NA_Z, ["oceania"], "na_oceania"), (NA_Z, ["south_america"], "na_south_america"),
    (NA_Z, ["me_india"], "na_me_india"),
    (["south_america"], ["africa"], "south_america_africa"), (["africa"], ["africa"], "intra_africa"),
    (["south_america"], ["south_america"], "intra_south_america"), (["me_india"], ["me_india"], "intra_me_india"),
]


def lanes(kind):
    out = [{"a": a, "b": b, "vehicle_type": f"glec/sea/container/{lid}/{kind}", "label": LANES[lid][0]}
           for a, b, lid in lane_rules]
    out.append({"any": True, "vehicle_type": f"glec/sea/container/other/{kind}", "label": LANES["other"][0]})
    return out


# ---------------------------------------------------------------- aliases
def by_region(**m):
    return {k: v for k, v in m.items()}


aliases = [
    {"mode": "road", "alias": "van", "vehicle_type": "glec/road/eu_sa/van_3_5t/diesel", "region_set": "land",
     "regions": by_region(asia_africa="glec/road/asia_africa/van_3_5t/diesel", na="glec/road/na/van",
                          cn="glec/road/cn/dump_3_5_4_5t/diesel", **{"in": "glec/road/in/small_cv/cng"}),
     "note": "GLEC's starting point for vans in the leg's region."},
    {"mode": "road", "alias": "truck_urban", "vehicle_type": "glec/road/eu_sa/rigid_3_5_7_5t/diesel", "region_set": "land",
     "regions": by_region(asia_africa="glec/road/asia_africa/rigid_3_5_7_5t/diesel", na="glec/road/na/ltl_dry_van",
                          cn="glec/road/cn/rigid_4_5_5_5t/diesel", **{"in": "glec/road/in/medium_cv_1/cng"}),
     "note": "GLEC's starting point for urban trucks (3.5-7.5 t) in the leg's region."},
]
HGV_REG = by_region(asia_africa="glec/road/asia_africa/rigid_26_32t/diesel", na="glec/road/na/tl_dry_van",
                    cn="glec/road/cn/artic_14_24t/lng", **{"in": "glec/road/in/heavy_cv_1/diesel"})
HGV_NOTE = "GLEC's starting point for HGVs in the leg's region (North America: SmartWay TL/Dry Van, as GLEC gives none)."
ARTIC_REG = by_region(asia_africa="glec/road/asia_africa/artic_34_40t/diesel", na="glec/road/na/tl_dry_van",
                      cn="glec/road/cn/artic_35_40t/diesel", **{"in": "glec/road/in/tractor_trailer/diesel"})
aliases += [
    {"mode": "road", "alias": "hgv", "vehicle_type": "glec/road/eu_sa/rigid_26_32t/diesel", "region_set": "land",
     "regions": HGV_REG, "note": HGV_NOTE,
     "load_model": "hgv_non_refrigerated_all_diesel/average_non_refrigerated_hgvs"},
    {"mode": "road", "alias": "hgv_rigid", "vehicle_type": "glec/road/eu_sa/rigid_26_32t/diesel", "region_set": "land",
     "regions": HGV_REG, "note": HGV_NOTE,
     "load_model": "hgv_non_refrigerated_all_diesel/average_non_refrigerated_rigids"},
    {"mode": "road", "alias": "hgv_artic", "vehicle_type": "glec/road/eu_sa/artic_34_40t/diesel", "region_set": "land",
     "regions": ARTIC_REG, "note": "GLEC articulated truck 34-40 t, average/mixed load, diesel, or the region's nearest row.",
     "load_model": "hgv_non_refrigerated_all_diesel/average_non_refrigerated_artics"},
    {"mode": "road", "alias": "hgv_artic_33t", "vehicle_type": "glec/road/eu_sa/artic_34_40t/diesel", "region_set": "land",
     "regions": ARTIC_REG, "note": "GLEC articulated truck 34-40 t, average/mixed load, diesel, or the region's nearest row.",
     "load_model": "hgv_non_refrigerated_all_diesel/articulated_33t"},
    {"mode": "road", "alias": "hgv_reefer", "vehicle_type": "glec/road/eu_sa/temperature_controlled/rigid_26_32t/diesel",
     "region_set": "land",
     "regions": by_region(asia_africa="glec/road/asia_africa/temperature_controlled/rigid_26_32t/diesel",
                          na="glec/road/na/refrigerated", cn="glec/road/cn/temperature_controlled/artic_14_24t/lng",
                          **{"in": "glec/road/in/temperature_controlled/heavy_cv_1/diesel"}),
     "note": "The region's HGV value with GLEC's +12% for temperature-controlled road freight (North America: SmartWay Refrigerated).",
     "load_model": "hgv_refrigerated_all_diesel/average_refrigerated_hgvs"},
    {"mode": "rail", "alias": "freight_train", "vehicle_type": "glec/rail/eu/average_traction_unknown", "region_set": "land",
     "regions": by_region(na="glec/rail/na/average", **{"in": "glec/rail/in/average"}),
     "note": "GLEC's regional rail average."},
    {"mode": "rail", "alias": "freight_train_diesel", "vehicle_type": "glec/rail/eu/diesel/average_mixed"},
    {"mode": "rail", "alias": "freight_train_electric", "vehicle_type": "glec/rail/eu/electric/average_mixed"},
    {"mode": "air", "alias": "air_freight", "vehicle_type": "glec/air/unknown/long_haul",
     "short_vehicle_type": "glec/air/unknown/short_haul", "short_below_km": 1500, "region_set": "land",
     "regions": by_region(**{"in": "glec/air/in/domestic"}),
     "note": "GLEC air, aircraft type unknown; flights within India use GLEC's Indian value."},
    {"mode": "air", "alias": "air_freighter", "vehicle_type": "glec/air/freighter/long_haul",
     "short_vehicle_type": "glec/air/freighter/short_haul", "short_below_km": 1500},
    {"mode": "air", "alias": "air_belly", "vehicle_type": "glec/air/belly/long_haul",
     "short_vehicle_type": "glec/air/belly/short_haul", "short_below_km": 1500},
    {"mode": "inland_waterway", "alias": "container_barge", "vehicle_type": "glec/iww/container_110m"},
    {"mode": "inland_waterway", "alias": "barge", "vehicle_type": "glec/iww/motor_vessel_85_110m"},
    {"mode": "sea", "alias": "container_ship", "vehicle_type": "glec/sea/container/industry_average/dry", "region_set": "sea",
     "lanes": lanes("dry"),
     "note": "GLEC container factor (Clean Cargo 2025) for the ports' trade lane."},
    {"mode": "sea", "alias": "container_ship_reefer", "vehicle_type": "glec/sea/container/industry_average/reefer",
     "region_set": "sea", "lanes": lanes("reefer"),
     "note": "GLEC reefer container factor (Clean Cargo 2025) for the ports' trade lane."},
]

# ---------------------------------------------------------------- hubs (Table 3, GILA project, kg CO2e per tonne)
HUBS = [
    ("transshipment", "transshipment, ambient (n = 172)", 1.2),
    ("transshipment_mixed", "transshipment, mixed ambient and temperature-controlled (n = 17)", 2.7),
    ("storage_transshipment", "storage + transshipment, ambient (n = 173)", 3.6),
    ("storage_transshipment_mixed", "storage + transshipment, mixed (n = 49)", 4.0),
    ("warehouse", "warehouse, ambient (n = 92)", 35.5),
    ("liquid_bulk_terminal", "liquid bulk terminal, ambient (n = 26)", 3.3),
    ("liquid_bulk_terminal_mixed", "liquid bulk terminal, mixed (n = 35)", 8.9),
]
hubs = [{"hub_type": h, "wtw_g_per_tonne": kg * 1000, "year": YEAR,
         "source": f"{GLEC}, Section 3 Module 2, Table 3, logistics hubs (GILA project: Fraunhofer IML, Politecnico di Milano, "
                   f"GreenRouter; median values): {label}"} for h, label, kg in HUBS]

dafs = [
    {"mode": "inland_waterway", "from": "actual", "to": "sfd", "multiplier": 1,
     "source": f"{GLEC}, Section 1 Chapter 4 (inland waterways, p. 44): there is little opportunity for deviation between the "
               f"actual distance and the shortest feasible distance, so no DAF is applied"},
    {"mode": "road", "from": "actual", "to": "sfd", "multiplier": 1 / 1.05,
     "source": f"{GLEC}, Section 3 Module 2: GLEC road values include a +5% distance conversion between actual and shortest "
               f"feasible distance, so an actual distance is divided by 1.05"},
    {"mode": "sea", "from": "actual", "to": "sfd", "multiplier": 1 / 1.15,
     "source": f"{GLEC}, Section 3 Module 2: GLEC container values include a +15% distance conversion between actual and "
               f"shortest feasible distance, so an actual distance is divided by 1.15"},
    {"mode": "sea", "from": "sfd", "to": "actual", "multiplier": 1.15,
     "source": f"{GLEC}, Section 3 Module 2: non-container sea values with a +15% distance conversion between actual and shortest feasible distance"},
]

dataset = {
    "name": "glec-3.3",
    "note": ("GLEC Framework v3.3 default values. Source: Smart Freight Centre. Global Logistics Emissions Council Framework "
             "for Logistics Emissions Accounting and Reporting; v3.3 edition, revised and updated (October 2026). "
             "ISBN 978-90-833629-0-8. Used with permission; Smart Freight Centre is acknowledged as the source. "
             "No affiliation with or endorsement by Smart Freight Centre is implied. " + LAND_NOTE + " " + SEA_NOTE),
    "legs": legs,
    "hubs": hubs,
    "fuels": [],
    "vehicle_aliases": aliases,
    "regions": regions,
    "container_loads": {"average": 10, "light": 6, "heavy": 14.5},
    "dafs": dafs,
}
with open(OUT, "w", encoding="utf-8") as f:
    json.dump(dataset, f, indent=1, ensure_ascii=False)
    f.write("\n")
print(f"wrote {OUT}: {len(legs)} factors, {len(hubs)} hubs, {len(aliases)} aliases, {len(regions)} region rules")
