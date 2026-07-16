#!/usr/bin/env python3
"""3/4-view dog breeds (shiba / retriever / pom) + round fluff-ball S0."""
from PIL import Image
import os

PAL = {
    'k': (0x2E, 0x2E, 0x2E),
    'e': (0x1A, 0x1A, 0x1A),
    'W': (0xFA, 0xFA, 0xFA),
    'l': (0xD9, 0xD9, 0xD9),
    'm': (0xA6, 0xA6, 0xA6),
    'd': (0x66, 0x66, 0x66),
}

# Shiba 3/4: upright ears, curled tail on the back, urajiro chest/muzzle
SHIBA = [
    "................................",
    "................................",
    "................................",
    "......kk......kk................",
    ".....kmmk....kmmk...............",
    ".....kmmmk..kmmmk...............",
    ".....kmmmmkkmmmmk...............",
    "....kmmmmmmmmmmmmk..............",
    "....kmmmmmmmmmmmmk..............",
    "....kmmmmmmmmmmmmk..............",
    "....kmmmmmmmmmmmmk..............",
    "....kmmeemmmmeemmk..............",
    "....kmmeemmmmeemmk..............",
    "....kmmeemmmmeemmk..............",
    "....kWWWWWeeWWmmmk......kkk.....",
    "....kWWWWWeeWWmmmmmmmmmmkmmk....",
    "....kmWWWWWWWmmmmmmmmmmmkmkmk...",
    "....kmWWWWmmmmmmmmmmmmmmkmmk....",
    "....kWWWWWWmmmmmmmmmmmmmkkk.....",
    "....kWWWWWWmmmmmmmmmmmmmk.......",
    "....kWWWWWWmmmmmmmmmmmmmk.......",
    "....kWWWWWWmmmmmmmmmmmmmk.......",
    "....kWWkWWmmmmmmmmmmmmmmk.......",
    "....kWWkWWmmmmmmmmmmmmmmk.......",
    "....kWWkWWmmmmmmmmmkmmmmk.......",
    "....kWWkWWmmmmmmmmmkmmmmk.......",
    ".....kkkkkkkkkkkkkkkkkkk........",
    "................................",
    "................................",
    "................................",
    "................................",
    "................................",
]

# Retriever 3/4: floppy ears, light coat, upright wagging tail
RETRIEVER = [
    "................................",
    "................................",
    "................................",
    "................................",
    "....kkkkkkkkkkkkk...............",
    "...kdkllllllllllkdk.............",
    "..kddklllllllllllkddk...........",
    "..kddklllllllllllkddk...........",
    "..kddklllllllllllkddk...........",
    "..kddklllllllllllkddk...........",
    "..kddklllllllllllkddk...........",
    "..kddkleelllleellkddk...........",
    "..kddkleelllleellkddk....kkk....",
    "..kkkkleelllleellkkkk...klllk...",
    "....kWWWWWeeWWlllk......klllk...",
    "....kWWWWWeeWWlllllllllkklllk...",
    "....klWWWWWWWlllllllllllklllk...",
    "....klWWWWlllllllllllllllkllk...",
    "....kWWWWWWllllllllllllllkkk....",
    "....kWWWWWWllllllllllllllk......",
    "....kWWWWWWlllllllllllllk.......",
    "....kWWWWWWlllllllllllllk.......",
    "....kWWkWWlllllllllllllk........",
    "....kWWkWWlllllllllllllk........",
    "....kWWkWWlllllllllklllk........",
    "....kWWkWWlllllllllklllk........",
    ".....kkkkkkkkkkkkkkkkkkk........",
    "................................",
    "................................",
    "................................",
    "................................",
    "................................",
]

# Pomeranian 3/4: tiny ears, scalloped fluff, big plume tail over the back
POM = [
    "................................",
    "................................",
    "................................",
    "......kk......kk................",
    ".....kWWk....kWWk...............",
    ".....kWWWkkkkWWWk...............",
    "....kWWWWWWWWWWWWk..............",
    "...kWWWWWWWWWWWWWWk.............",
    "....kWWWWWWWWWWWWk....kkkk......",
    "...kWWWWWWWWWWWWWWk..kWWWWk.....",
    "....kWWeeWWWWeeWWk..kWWWWWWk....",
    "...kWWWeeWWWWeeWWWk.kWWWWWWk....",
    "....kWWeeWWWWeeWWk..kWWWWWWWk...",
    "...kWWWWWeeWWWWWWWk.kWWWWWWk....",
    "....kWWWWWWWWWWWWWWkkWWWWWWk....",
    "...kWWWWWWWWWWWWWWWWWWWWWWk.....",
    "....kWWWWWWWWWWWWWWWWWWWWWk.....",
    "...kWWWWWWWWWWWWWWWWWWWWWk......",
    "....kWWWWWWWWWWWWWWWWWWWWWk.....",
    "...kWWWWWWWWWWWWWWWWWWWWWk......",
    "....kWWWWWWWWWWWWWWWWWWWWWk.....",
    "...kWWlWWWWWWWWWWWWWWWlWWWk.....",
    "....kWWWWWWWWWWWWWWWWWWWWk......",
    "....kkWWkkWWWWWWWWWWkkWWkk......",
    "......kk..kkkkkkkkkk..kk........",
    "................................",
    "................................",
    "................................",
    "................................",
    "................................",
    "................................",
    "................................",
]

# S0: round fluffy fur ball (no face — just a pom-pom)
FLUFF = [
    "................................",
    "................................",
    "................................",
    "................................",
    "................................",
    "................................",
    "................................",
    "................................",
    "................................",
    "................................",
    "................................",
    "................................",
    "............kkkkkkkk............",
    "..........kkllllllllkk..........",
    ".........kllllllllllllk.........",
    ".......kkllllllllllllllkk.......",
    "........kllllllmmllllllk........",
    ".......kllllllllllllllllk.......",
    "........kllllllllllmmllk........",
    ".......kllllllllllllllllk.......",
    "........kllmmllllllllllk........",
    ".......kllllllllllllllllk.......",
    "........klllllllllmmlllk........",
    ".......kkllllllllllllllkk.......",
    ".........kllllllllllllk.........",
    "..........kkllllllllkk..........",
    "............kkkkkkkk............",
    "................................",
    "................................",
    "................................",
    "................................",
    "................................",
]


def render(rows, out, scale=8, bg=(240, 240, 240, 255)):
    for i, r in enumerate(rows):
        if len(r) != 32:
            raise SystemExit(f"{out}: row {i} has length {len(r)}")
        bad = set(r) - set(PAL) - {'.'}
        if bad:
            raise SystemExit(f"{out}: row {i} unknown chars {bad}")
    img = Image.new('RGBA', (32, len(rows)), (0, 0, 0, 0))
    for y, row in enumerate(rows):
        for x, ch in enumerate(row):
            if ch in PAL:
                img.putpixel((x, y), PAL[ch] + (255,))
    img = img.resize((32 * scale, len(rows) * scale), Image.NEAREST)
    c = Image.new('RGBA', img.size, bg)
    c.alpha_composite(img)
    c.save(out)


if __name__ == '__main__':
    d = os.path.dirname(os.path.abspath(__file__))
    os.chdir(d)
    strip = Image.new('RGBA', (32 * 8 * 4 + 48, 32 * 8), (240, 240, 240, 255))
    for i, (name, g) in enumerate([('fluff', FLUFF), ('shiba34', SHIBA), ('ret34', RETRIEVER), ('pom34', POM)]):
        render(g, f"{name}_8x.png")
        strip.alpha_composite(Image.open(f"{name}_8x.png"), (i * (32 * 8 + 16), 0))
    strip.save('breeds34_strip.png')
    print('ok')
