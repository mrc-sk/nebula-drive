#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""NebulaDrive 宣传片配乐 v2：120 BPM 强节奏电子 / trailer 结构。
分段：Intro → Groove → Build → Drop → Break → Drop2 → Finale。
含 four-on-the-floor kick、clap、16 分 hihat、律动 bass、sidechain pad、
琶音、主旋律、riser 与 impact。全部 numpy 原创合成，无外部素材。
"""
import numpy as np
from scipy.io import wavfile

SR = 44100
DUR = 240.0
BPM = 120
BEAT = 60.0 / BPM          # 0.5 s
N = int(SR * DUR)
out = np.zeros(N, np.float32)
rng = np.random.default_rng(20261114)

# 每 16 秒一段（= 8 小节），共 15 段
LEVELS = [0.18, 0.46, 0.56, 0.62, 0.70, 0.78, 0.88,
          1.00, 1.00, 0.96, 0.44, 0.42, 1.00, 1.00, 0.90]
SEG = 16.0

# i - VI - III - VII（Am - F - C - G），每和弦 8 秒
CHORDS = [
    (220.00, 261.63, 329.63),
    (174.61, 220.00, 261.63),
    (261.63, 329.63, 392.00),
    (196.00, 246.94, 293.66),
]
ROOTS = [110.00, 87.31, 130.81, 98.00]
MOTIF = [440.00, 523.25, 659.25, 783.99, 659.25, 523.25, 587.33, 523.25]


def seg(t):
    return min(len(LEVELS) - 1, int(t / SEG))


def chord(t):
    return int(t // 8.0) % 4


def add(t0, sig, gain=1.0):
    i0 = int(t0 * SR)
    i1 = min(N, i0 + len(sig))
    if i1 > i0:
        out[i0:i1] += sig[: i1 - i0] * np.float32(gain)


def hp_noise(n, decay, seed=None):
    x = rng.standard_normal(n + 1).astype(np.float32)
    h = np.diff(x)
    tt = np.arange(n, dtype=np.float32) / SR
    return h * np.exp(-tt * decay)


# ---------- 1) Kick：four-on-the-floor ----------
for k in range(int(DUR / BEAT)):
    t0 = k * BEAT
    L = LEVELS[seg(t0)]
    if L < 0.30 and k % 2:          # intro 段稀疏
        continue
    n = int(0.30 * SR)
    tt = np.arange(n, dtype=np.float32) / SR
    f = 150 * np.exp(-tt * 28) + 46
    phase = 2 * np.pi * np.cumsum(f) / SR
    sig = (np.sin(phase) * np.exp(-tt * 10) * 0.95
           + 0.12 * rng.standard_normal(n) * np.exp(-tt * 130))
    add(t0, sig.astype(np.float32), 0.92 * (0.55 + 0.45 * L))

# ---------- 2) Clap / Snare：每小节第 2、4 拍 ----------
for bar in range(int(DUR / 2.0)):
    for off in (0.5, 1.5):
        t0 = bar * 2.0 + off
        L = LEVELS[seg(t0)]
        if L < 0.40:
            continue
        n = int(0.20 * SR)
        sig = hp_noise(n, 20) * 0.42
        add(t0, sig, 0.80 * L)

# ---------- 3) Hihat：8 分为底，drop 段叠 16 分 ----------
for k in range(int(DUR / 0.125)):
    t0 = k * 0.125
    L = LEVELS[seg(t0)]
    if L < 0.40:
        continue
    if k % 2 and L < 0.85:          # 低能量段只走 8 分
        continue
    n = int(0.055 * SR)
    acc = 1.0 if k % 2 == 0 else 0.55
    sig = hp_noise(n, 62) * 0.22 * acc
    add(t0, sig, 0.55 * L)

# ---------- 4) Bass：8 分律动，跟和弦根音 ----------
for k in range(int(DUR / 0.25)):
    t0 = k * 0.25
    L = LEVELS[seg(t0)]
    if L < 0.35:
        continue
    f = ROOTS[chord(t0)]
    n = int(0.24 * SR)
    tt = np.arange(n, dtype=np.float32) / SR
    sig = ((np.sin(2 * np.pi * f * tt) + 0.34 * np.sin(2 * np.pi * 2 * f * tt)
            + 0.12 * np.sin(2 * np.pi * 3 * f * tt))
           * np.exp(-tt * 5.0) * 0.52)
    add(t0, sig.astype(np.float32), 0.62 * (0.5 + 0.5 * L))

# ---------- 5) Pad：每和弦 8 秒，带 sidechain duck ----------
for ci in range(int(DUR / 8.0)):
    t0 = ci * 8.0
    ch = CHORDS[ci % 4]
    L = LEVELS[seg(t0)]
    n = int(8.0 * SR)
    tt = np.arange(n, dtype=np.float32) / SR
    sig = np.zeros(n, np.float32)
    for f in ch:
        for harm, amp in ((1, 1.0), (2, 0.36), (3, 0.12)):
            sig += amp * np.sin(2 * np.pi * f * harm * tt)
        sig += 0.28 * np.sin(2 * np.pi * f * 1.004 * tt)
    env = np.minimum(np.minimum(tt / 0.9, (8.0 - tt) / 1.0), 1.0)
    sig *= np.clip(env, 0, 1) ** 1.2
    duck = np.ones(n, np.float32)
    bl = int(BEAT * SR)
    for b in range(16):
        i0 = b * bl
        ln = min(bl, n - i0)
        duck[i0:i0 + ln] = 1 - 0.34 * np.exp(-np.arange(ln, dtype=np.float32) / SR * 15)
    sig *= duck
    add(t0, sig * 0.052, 0.55 + 0.45 * L)

# ---------- 6) Arp：8 分音符，和弦音高八度 ----------
for k in range(int(DUR / 0.25)):
    t0 = k * 0.25
    L = LEVELS[seg(t0)]
    if L < 0.32:
        continue
    ch = CHORDS[chord(t0)]
    f = ch[k % 3] * 2.0
    n = int(0.20 * SR)
    tt = np.arange(n, dtype=np.float32) / SR
    sig = ((np.sin(2 * np.pi * f * tt) + 0.28 * np.sin(2 * np.pi * 2 * f * tt))
           * np.exp(-tt * 13) * 0.30)
    add(t0, sig.astype(np.float32), 0.46 * L)

# ---------- 7) Lead：drop 段主旋律 ----------
for k in range(int((DUR - 112) / 0.5)):
    t0 = 112 + k * 0.5
    L = LEVELS[seg(t0)]
    if L < 0.80:
        continue
    f = MOTIF[k % len(MOTIF)]
    n = int(0.42 * SR)
    tt = np.arange(n, dtype=np.float32) / SR
    sig = ((np.sin(2 * np.pi * f * tt) + 0.22 * np.sin(2 * np.pi * 2 * f * tt)
            + 0.10 * np.sin(2 * np.pi * 3 * f * tt))
           * np.minimum(tt / 0.02, 1) * np.exp(-tt * 3.2) * 0.34)
    add(t0, sig.astype(np.float32), 0.78 * L)

# ---------- 8) Riser / Impact 转场 ----------
for st in (110.0, 190.0):
    n = int(2.0 * SR)
    tt = np.arange(n, dtype=np.float32) / SR
    rise = (tt / 2.0) ** 2
    noise = hp_noise(n, 1.2) * rise * 0.30
    sweep = np.sin(2 * np.pi * (220 + 660 * rise) * tt) * rise * 0.18
    add(st, (noise + sweep).astype(np.float32), 0.9)
for st in (112.0, 192.0):
    n = int(1.3 * SR)
    tt = np.arange(n, dtype=np.float32) / SR
    boom = np.sin(2 * np.pi * (110 * np.exp(-tt * 6) + 38) * tt) * np.exp(-tt * 3.5) * 0.85
    crash = hp_noise(n, 3.0) * 0.30
    add(st, (boom + crash).astype(np.float32), 0.95)

# ---------- 9) 空间感 / 软限幅 / 淡入淡出 ----------
d1, d2 = int(0.26 * SR), int(0.49 * SR)
rev = out.copy()
rev[d1:] += 0.14 * out[:-d1]
rev[d2:] += 0.07 * out[:-d2]
out = np.tanh(rev * 1.45).astype(np.float32) / 1.45

fi, fo = int(2.0 * SR), int(4.0 * SR)
out[:fi] *= np.linspace(0, 1, fi, dtype=np.float32) ** 1.5
out[-fo:] *= np.linspace(1, 0, fo, dtype=np.float32) ** 1.6
out /= max(float(np.max(np.abs(out))), 1e-6)
out *= 0.76

# 轻微立体声宽度：鼓居中，pad/arp 展开
d = int(0.007 * SR)
L = out
R = np.concatenate([np.zeros(d, np.float32), out[:-d]]) * 0.90 + out * 0.10
stereo = np.stack([L, R], axis=1)
wavfile.write("/workspace/promo/assets/bgm.wav", SR,
              (np.clip(stereo, -1, 1) * 32767).astype(np.int16))
print("BGM v2 OK  %.0fs  120BPM  %d samples  peak=%.3f" % (DUR, N, float(np.max(np.abs(out)))))
