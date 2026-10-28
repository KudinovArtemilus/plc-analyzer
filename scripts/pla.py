"""Чтение файлов смены PLC Analyzer (.pla) в Python.

Формат (internal/archive/format.go):
  "PLCA" | uint32 длина JSON | JSON-заголовок | порции: [uint32 длина][uint32 CRC32][deflate]
"""
import json, struct, zlib
from datetime import datetime
import pandas as pd


LOCAL = datetime.now().astimezone().tzinfo  # часовой пояс Windows


def _uvarint(b, i):
    x = s = 0
    while True:
        c = b[i]; i += 1
        x |= (c & 0x7F) << s
        if c < 0x80:
            return x, i
        s += 7


def _varint(b, i):  # zigzag, как binary.AppendVarint в Go
    u, i = _uvarint(b, i)
    return (u >> 1) ^ -(u & 1), i


def read_pla(path):
    """Вернуть (заголовок, {номер группы: DataFrame}). Индекс — время, столбцы — сигналы."""
    with open(path, "rb") as f:
        data = f.read()
    if data[:4] != b"PLCA":
        raise ValueError("не файл PLC Analyzer")
    n = struct.unpack(">I", data[4:8])[0]
    hdr = json.loads(data[8:8 + n])
    pos = 8 + n
    parts = {}  # группа -> список (times, columns)
    while pos + 8 <= len(data):
        ln, crc = struct.unpack(">II", data[pos:pos + 8])
        payload = data[pos + 8:pos + 8 + ln]
        if len(payload) < ln or zlib.crc32(payload) != crc:
            break  # хвост оборван (файл ещё пишется) — читаем, что успели
        pos += 8 + ln
        raw = zlib.decompress(payload, -15)  # flate без заголовка zlib
        g, i = _uvarint(raw, 0)
        cnt, i = _uvarint(raw, i)
        times, t = [], 0
        for _ in range(cnt):
            d, i = _varint(raw, i); t += d; times.append(t)
        cols = []
        for _ in hdr["groups"][g]["signals"]:
            kind = raw[i]; i += 1
            if kind == 0:  # целые: разности varint
                col, v = [], 0
                for _ in range(cnt):
                    d, i = _varint(raw, i); v += d; col.append(v)
            else:          # дробные: 8 байт big-endian
                col = list(struct.unpack(f">{cnt}d", raw[i:i + 8 * cnt])); i += 8 * cnt
            cols.append(col)
        parts.setdefault(g, []).append((times, cols))
    frames = {}
    for g, chunks in parts.items():
        names = [s["name"] for s in hdr["groups"][g]["signals"]]
        dfs = [pd.DataFrame(dict(zip(names, c)), index=pd.to_datetime(t, unit="ms", utc=True))
               for t, c in chunks]
        frames[g] = pd.concat(dfs).tz_convert(LOCAL)  # время этого компьютера, как в вебе
    return hdr, frames