"""Regenerate tiny lossless PQ fixtures with an installed FFmpeg/libx265."""

from array import array
from pathlib import Path
import subprocess
import tempfile


root = Path(__file__).resolve().parent
width, height = 192, 112
cuts = [0, 12, 36, 60, 84, 108]
with tempfile.TemporaryDirectory(prefix="upbrr-pq-fixtures-") as scratch:
    raw = Path(scratch) / "pictures.yuv"
    for fps, first, name in [
        (24, 0.12, "decoded-pq-cuts.mkv"),
        (25, 0.22, "decoded-pq-cuts-25.mkv"),
    ]:
        levels = [first, 0.64, 0.22, 0.78, 0.32, 0.88]
        with raw.open("wb") as output:
            for index in range(120):
                level = levels[max(j for j, start in enumerate(cuts) if index >= start)]
                y = array("H", (
                    64 if row < 12 or row >= 100 else round(64 + 876 * level)
                    for row in range(height) for _ in range(width)
                ))
                output.write(y.tobytes())
                output.write(array("H", [512] * (width * height // 2)).tobytes())
        subprocess.run([
            "ffmpeg", "-hide_banner", "-nostdin", "-loglevel", "warning", "-y",
            "-f", "rawvideo", "-pixel_format", "yuv420p10le", "-video_size", "192x112",
            "-framerate", str(fps), "-i", str(raw), "-c:v", "libx265", "-preset", "ultrafast",
            "-x265-params", "lossless=1:pools=none:frame-threads=1:wpp=0:bframes=2:b-adapt=0:keyint=60:scenecut=0:colorprim=bt2020:transfer=smpte2084:colormatrix=bt2020nc:range=limited:sar=1",
            "-vf", "setsar=1", "-an", str(root / name),
        ], check=True)
