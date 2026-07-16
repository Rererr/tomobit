#!/usr/bin/env python3
"""Full stage expansion, 3/4 pose: shiba / retriever / pom x S0-S5."""
from PIL import Image
import os, json

from breeds34 import PAL, SHIBA, RETRIEVER, POM, FLUFF

BLANK = '.' * 32

def put(rows, r, c, ch):
    rows[r] = rows[r][:c] + ch + rows[r][c + 1:]

def blink(rows, top, body):
    b = list(rows)
    for r in (top, top + 2):
        b[r] = b[r].replace('e', body)
    return b

def star(rows, r, c):
    put(rows, r, c, 'm')
    for cc in (c - 1, c, c + 1):
        put(rows, r + 1, cc, 'm')
    put(rows, r + 2, c, 'm')

# ---- S0: fluff ball (shared). B = fur shimmer ----

FLUFF_A = list(FLUFF)
FLUFF_B = list(FLUFF)
FLUFF_B[16] = "........klllllmmlllllllk........"
FLUFF_B[18] = "........klllllllllmmlllk........"
FLUFF_B[20] = "........klllmmlllllllllk........"
FLUFF_B[22] = "........kllllllllmmllllk........"

# ---- S1: babies (big head, stubby body, small tail; pom plume not yet grown) ----

SHIBA_S1A = [BLANK] * 11 + [
    "......kk......kk................",
    ".....kmmk....kmmk...............",
    ".....kmmmk..kmmmk...............",
    ".....kmmmmkkmmmmk...............",
    "....kmmmmmmmmmmmmk..............",
    "....kmmmmmmmmmmmmk..............",
    "....kmmeemmmmeemmk..............",
    "....kmmeemmmmeemmk..............",
    "....kmmeemmmmeemmk..............",
    "....kWWWWWeeWWmmmk....kk........",
    "....kWWWWWeeWWmmmmmmmkmmk.......",
    "....kmWWWWWWWmmmmmmmkkk.........",
    "....kWWWWWWmmmmmmmmmk...........",
    "....kWWWWWWmmmmmmmmmk...........",
    "....kWWkWWmmmmmmmmmmk...........",
    ".....kkkkkkkkkkkkkkkk...........",
] + [BLANK] * 5

RET_S1A = [BLANK] * 13 + [
    "....kkkkkkkkkkkkk...............",
    "...kdkllllllllllkdk.............",
    "..kddklllllllllllkddk...........",
    "..kddklllllllllllkddk...........",
    "..kddkleelllleellkddk...........",
    "..kddkleelllleellkddk...........",
    "..kkkkleelllleellkkkk...........",
    "....kWWWWWeeWWlllk....kk........",
    "....kWWWWWeeWWlllllllklk........",
    "....klWWWWWWWlllllllkkk.........",
    "....kWWWWWWlllllllllk...........",
    "....kWWkWWlllllllllllk..........",
    ".....kkkkkkkkkkkkkkkk...........",
] + [BLANK] * 6

POM_S1A = [BLANK] * 12 + [
    "......kk......kk................",
    ".....kWWk....kWWk...............",
    ".....kWWWkkkkWWWk...............",
    "....kWWWWWWWWWWWWk..............",
    "...kWWWWWWWWWWWWWWk.............",
    "....kWWeeWWWWeeWWk..............",
    "...kWWWeeWWWWeeWWWk.............",
    "....kWWeeWWWWeeWWk..............",
    "...kWWWWWeeWWWWWWWk.............",
    "....kWWWWWWWWWWWWWWk............",
    "...kWWWWWWWWWWWWWWWWk...........",
    "....kWWWWWWWWWWWWWWk............",
    "....kkWWkkWWWWWWkkWWkk..........",
    "......kk..kkkkkk..kk............",
] + [BLANK] * 6

SHIBA_S1B = blink(SHIBA_S1A, 17, 'm')
RET_S1B = blink(RET_S1A, 17, 'l')
POM_S1B = blink(POM_S1A, 17, 'W')

# ---- S2: S3 minus 4 body rows, pad top 4, sprout on the head ----

SHIBA_S2A = [BLANK] * 4 + SHIBA[0:19] + SHIBA[23:]
RET_S2A = [BLANK] * 4 + RETRIEVER[0:19] + RETRIEVER[23:]
POM_S2A = [BLANK] * 4 + POM[0:17] + POM[21:]

SPROUT = {  # (stem(r,c), leafA(r,c), leafB(r,c))
    'shiba': ((9, 11), (8, 12), (8, 10)),
    'ret': ((7, 10), (6, 11), (6, 9)),
    'pom': ((8, 10), (7, 11), (7, 9)),
}
for g, key in ((SHIBA_S2A, 'shiba'), (RET_S2A, 'ret'), (POM_S2A, 'pom')):
    stem, leaf_a, _ = SPROUT[key]
    put(g, *stem, 'm')
    put(g, *leaf_a, 'm')

def s2_b(a, key, top, body):
    b = blink(a, top, body)
    _, leaf_a, leaf_b = SPROUT[key]
    put(b, leaf_a[0], leaf_a[1], '.')
    put(b, leaf_b[0], leaf_b[1], 'm')
    return b

SHIBA_S2B = s2_b(SHIBA_S2A, 'shiba', 15, 'm')
RET_S2B = s2_b(RET_S2A, 'ret', 15, 'l')
POM_S2B = s2_b(POM_S2A, 'pom', 14, 'W')

# ---- S3: approved studies ----

SHIBA_S3A, RET_S3A, POM_S3A = list(SHIBA), list(RETRIEVER), list(POM)
SHIBA_S3B = blink(SHIBA_S3A, 11, 'm')
RET_S3B = blink(RET_S3A, 11, 'l')
POM_S3B = blink(POM_S3A, 10, 'W')

# ---- S4: chest bandana (band 2 rows over cols 5-16 + knot triangle) ----

def bandana(s3, band_rows, band_lo=5, band_hi=16):
    g = list(s3)
    n = band_hi - band_lo + 1
    for r in band_rows:
        g[r] = g[r][:band_lo] + 'd' * n + g[r][band_hi + 1:]
    t0 = band_rows[-1] + 1
    g[t0] = g[t0][:8] + 'dddddd' + g[t0][14:]
    g[t0 + 1] = g[t0 + 1][:9] + 'dddd' + g[t0 + 1][13:]
    g[t0 + 2] = g[t0 + 2][:10] + 'dd' + g[t0 + 2][12:]
    return g

SHIBA_S4A = bandana(SHIBA_S3A, (17, 18))
RET_S4A = bandana(RET_S3A, (17, 18))
POM_S4A = bandana(POM_S3A, (15, 16), band_lo=4)

SHIBA_S4B = blink(SHIBA_S4A, 11, 'm')
RET_S4B = blink(RET_S4A, 11, 'l')
POM_S4B = blink(POM_S4A, 10, 'W')

# ---- S5: S4 + twin stars (grayscale 'm', swap positions in frame B) ----

S5_STARS = {  # (A-left, A-right, B-left, B-right)
    'shiba': ((6, 2), (4, 20), (3, 2), (8, 20)),
    'ret': ((2, 1), (5, 23), (20, 1), (2, 23)),
    'pom': ((4, 1), (3, 19), (8, 1), (6, 19)),
}

def s5(key, s4a, top, body):
    al, ar, bl, br = S5_STARS[key]
    a = list(s4a)
    star(a, *al)
    star(a, *ar)
    b = blink(s4a, top, body)
    star(b, *bl)
    star(b, *br)
    return a, b

SHIBA_S5A, SHIBA_S5B = s5('shiba', SHIBA_S4A, 11, 'm')
RET_S5A, RET_S5B = s5('ret', RET_S4A, 11, 'l')
POM_S5A, POM_S5B = s5('pom', POM_S4A, 10, 'W')

ALL = {
    'S0': {'shared': (FLUFF_A, FLUFF_B)},
    'S1': {'shiba': (SHIBA_S1A, SHIBA_S1B), 'ret': (RET_S1A, RET_S1B), 'pom': (POM_S1A, POM_S1B)},
    'S2': {'shiba': (SHIBA_S2A, SHIBA_S2B), 'ret': (RET_S2A, RET_S2B), 'pom': (POM_S2A, POM_S2B)},
    'S3': {'shiba': (SHIBA_S3A, SHIBA_S3B), 'ret': (RET_S3A, RET_S3B), 'pom': (POM_S3A, POM_S3B)},
    'S4': {'shiba': (SHIBA_S4A, SHIBA_S4B), 'ret': (RET_S4A, RET_S4B), 'pom': (POM_S4A, POM_S4B)},
    'S5': {'shiba': (SHIBA_S5A, SHIBA_S5B), 'ret': (RET_S5A, RET_S5B), 'pom': (POM_S5A, POM_S5B)},
}


def validate():
    for stage, d in ALL.items():
        for sp, (a, b) in d.items():
            for label, g in (('A', a), ('B', b)):
                if len(g) != 32:
                    raise SystemExit(f"{stage}/{sp}/{label}: {len(g)} rows")
                for i, r in enumerate(g):
                    if len(r) != 32:
                        raise SystemExit(f"{stage}/{sp}/{label} row {i}: len {len(r)}")
                    bad = set(r) - set(PAL) - {'.'}
                    if bad:
                        raise SystemExit(f"{stage}/{sp}/{label} row {i}: {bad}")


def to_img(g, scale, bg):
    img = Image.new('RGBA', (32, 32), (0, 0, 0, 0))
    for y, row in enumerate(g):
        for x, ch in enumerate(row):
            if ch in PAL:
                img.putpixel((x, y), PAL[ch] + (255,))
    img = img.resize((32 * scale, 32 * scale), Image.NEAREST)
    c = Image.new('RGBA', img.size, bg)
    c.alpha_composite(img)
    return c


def sheet(frame, out, scale=5):
    cell = 32 * scale
    pad = 8
    img = Image.new('RGBA', (6 * (cell + pad) + pad, 3 * (cell + pad) + pad), (240, 240, 240, 255))
    for row, sp in enumerate(['shiba', 'ret', 'pom']):
        for col, stage in enumerate(['S0', 'S1', 'S2', 'S3', 'S4', 'S5']):
            g = ALL[stage].get(sp) or ALL[stage]['shared']
            img.alpha_composite(to_img(g[frame], scale, (240, 240, 240, 255)),
                                (pad + col * (cell + pad), pad + row * (cell + pad)))
    img.save(out)
    print(out)


if __name__ == '__main__':
    validate()
    d = os.path.dirname(os.path.abspath(__file__))
    os.chdir(d)
    sheet(0, 'stages34_sheet_A.png')
    sheet(1, 'stages34_sheet_B.png')
    with open('stages34.json', 'w') as f:
        json.dump({st: {sp: [''.join(a), ''.join(b)] for sp, (a, b) in d2.items()}
                   for st, d2 in ALL.items()}, f)
    print('ok')
