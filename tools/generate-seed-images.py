#!/usr/bin/env python3
"""Render a placeholder image for every asset the seed fixture references.

The fixture used to point at https://cdn.nusantara.test/..., a .test host that
by RFC 6761 can never resolve, so every product row rendered as a broken image.
These are deliberately not photographs: they are typographic cards that read as
demo data rather than pretending to be a real product shot.

Run from the service root:

    python3 tools/generate-seed-images.py            # writes images/
    python3 tools/generate-seed-images.py --force    # redraw existing files

The target list comes from the Go seeder itself (`go run ./cmd/seed-assets
-list`), so adding a product to the fixture automatically adds its image.
"""

from __future__ import annotations

import argparse
import colorsys
import hashlib
import json
import subprocess
import sys
from pathlib import Path

from PIL import Image, ImageDraw, ImageFont

ROOT = Path(__file__).resolve().parent.parent
OUTPUT = ROOT / "images"

FONT_BOLD = "/usr/share/fonts/truetype/dejavu/DejaVuSans-Bold.ttf"
FONT_REGULAR = "/usr/share/fonts/truetype/dejavu/DejaVuSans.ttf"

# Per-folder canvas sizes, matching how each image is actually displayed.
SIZES = {
    "products": (800, 800),
    "types": (600, 600),
    "shops": (1200, 675),
    "banners": (1200, 480),
    "events": (1200, 600),
    "avatars": (400, 400),
}


def accent_colour(accent: str) -> tuple[tuple[int, int, int], tuple[int, int, int]]:
    """Derive a stable pair of background colours from an accent key.

    Hashing rather than a lookup table means a new category gets a distinct
    colour without anyone maintaining a palette.
    """
    digest = hashlib.sha256(accent.encode()).digest()
    hue = digest[0] / 255.0

    top = colorsys.hls_to_rgb(hue, 0.30, 0.42)
    bottom = colorsys.hls_to_rgb(hue, 0.17, 0.48)
    to_rgb = lambda c: tuple(int(round(v * 255)) for v in c)
    return to_rgb(top), to_rgb(bottom)


def vertical_gradient(size: tuple[int, int], top: tuple[int, int, int],
                      bottom: tuple[int, int, int]) -> Image.Image:
    """A gradient gives the card depth that a flat fill does not."""
    width, height = size
    base = Image.new("RGB", (1, height))
    pixels = base.load()

    for y in range(height):
        ratio = y / max(height - 1, 1)
        pixels[0, y] = tuple(
            int(round(top[i] + (bottom[i] - top[i]) * ratio)) for i in range(3)
        )

    return base.resize(size, Image.BILINEAR)


def wrap(draw: ImageDraw.ImageDraw, text: str, font: ImageFont.FreeTypeFont,
         max_width: int) -> list[str]:
    lines: list[str] = []
    current = ""

    for word in text.split():
        candidate = f"{current} {word}".strip()
        if draw.textlength(candidate, font=font) <= max_width or not current:
            current = candidate
        else:
            lines.append(current)
            current = word

    if current:
        lines.append(current)
    return lines


def fitted_font(draw: ImageDraw.ImageDraw, text: str, path: str,
                max_width: int, max_height: int, start: int) -> tuple[ImageFont.FreeTypeFont, list[str]]:
    """Shrink the type until the wrapped block fits the space it has."""
    size = start
    while size > 14:
        font = ImageFont.truetype(path, size)
        lines = wrap(draw, text, font, max_width)
        line_height = int(size * 1.25)
        if len(lines) * line_height <= max_height:
            return font, lines
        size -= 2

    font = ImageFont.truetype(path, 14)
    return font, wrap(draw, text, font, max_width)


def initials(name: str) -> str:
    parts = [p for p in name.split() if p]
    return "".join(p[0] for p in parts[:2]).upper() or "?"


def draw_avatar(target: dict, size: tuple[int, int]) -> Image.Image:
    top, bottom = accent_colour(target["accent"])
    image = vertical_gradient(size, top, bottom)
    draw = ImageDraw.Draw(image)

    font = ImageFont.truetype(FONT_BOLD, int(size[1] * 0.38))
    text = initials(target["title"])
    box = draw.textbbox((0, 0), text, font=font)

    draw.text(
        ((size[0] - (box[2] - box[0])) / 2 - box[0],
         (size[1] - (box[3] - box[1])) / 2 - box[1]),
        text, font=font, fill=(255, 255, 255),
    )
    return image


def draw_card(target: dict, size: tuple[int, int]) -> Image.Image:
    top, bottom = accent_colour(target["accent"])
    image = vertical_gradient(size, top, bottom)
    draw = ImageDraw.Draw(image)

    width, height = size
    margin = int(width * 0.09)
    inner = width - margin * 2

    # A hairline frame keeps the composition from floating in the gradient.
    draw.rectangle(
        [margin // 2, margin // 2, width - margin // 2, height - margin // 2],
        outline=(255, 255, 255, 40), width=2,
    )

    title_font, title_lines = fitted_font(
        draw, target["title"], FONT_BOLD, inner, int(height * 0.42), int(height * 0.13)
    )
    line_height = int(title_font.size * 1.22)
    block_height = len(title_lines) * line_height

    y = (height - block_height) / 2 - int(height * 0.02)
    for line in title_lines:
        draw.text(((width - draw.textlength(line, font=title_font)) / 2, y),
                  line, font=title_font, fill=(255, 255, 255))
        y += line_height

    if target.get("subtitle"):
        sub_font = ImageFont.truetype(FONT_REGULAR, max(int(height * 0.045), 16))
        sub = target["subtitle"].upper()
        # Letterspacing reads as deliberate rather than as a squeezed caption.
        spaced = " ".join(sub)
        draw.text(((width - draw.textlength(spaced, font=sub_font)) / 2, y + int(height * 0.03)),
                  spaced, font=sub_font, fill=(255, 255, 255, 190))

    return image


def load_targets() -> list[dict]:
    result = subprocess.run(
        ["go", "run", "./cmd/seed-assets", "-list"],
        cwd=ROOT, capture_output=True, text=True,
    )
    if result.returncode != 0:
        sys.exit(f"could not read the target list:\n{result.stderr}")
    return json.loads(result.stdout)


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--force", action="store_true", help="redraw images that already exist")
    args = parser.parse_args()

    targets = load_targets()
    written = skipped = 0

    for target in targets:
        folder = target["folder"]
        path = OUTPUT / folder / f"{target['key']}.png"

        if path.exists() and not args.force:
            skipped += 1
            continue

        path.parent.mkdir(parents=True, exist_ok=True)
        size = SIZES.get(folder, (800, 800))

        image = draw_avatar(target, size) if folder == "avatars" else draw_card(target, size)
        image.save(path, "PNG", optimize=True)
        written += 1

    total = sum(1 for _ in OUTPUT.rglob("*.png"))
    print(f"{written} written, {skipped} already present, {total} file(s) in {OUTPUT.relative_to(ROOT)}/")


if __name__ == "__main__":
    main()
