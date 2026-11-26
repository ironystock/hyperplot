"""data.py: the example's stand-in data, and a clock that can run fast.

Every value is a pure function of (series, bucket index): the same bucket
always has the same value, whatever the request order or the time of the
request. That makes a frame's bytes depend only on its window, so a poll
inside an unchanged window hashes to the same tag and gets a 304
(spec §4.4, §4.5), and the tests can pin any instant.
"""

from __future__ import annotations

import math
import time
import zlib
from typing import Callable

LATENCY_STEP = 10  # seconds per latency bucket
WINDOW = 300  # seconds per requests window (five minutes)
REGIONS = ('us-east', 'eu-west', 'ap-south')
METHODS = ('GET', 'POST')


def noise(series: str, n: int) -> float:
    """A pseudo-random number in [0, 1): a pure function of (series, n)."""
    return zlib.crc32(f'{series}:{n}'.encode()) / 2 ** 32


def latency(n: int) -> tuple[float, float]:
    """p50 and p95 latency in ms of latency bucket n (it starts at n * 10 s)."""
    p50 = 42 + 6 * math.sin(n / 9) + 5 * noise('p50', n)
    p95 = p50 + 38 + 14 * math.sin(n / 23) + 30 * noise('p95', n) ** 3
    return round(p50, 1), round(p95, 1)


def throughput(n: int) -> tuple[float, float]:
    """GET and POST requests per second during second n."""
    get = 180 + 40 * math.sin(n / 30) + 25 * noise('GET', n)
    post = 60 + 15 * math.sin(n / 47) + 12 * noise('POST', n)
    return round(get), round(post)


def requests_per_minute(region: str, minute: int) -> int:
    """Requests that region served during minute number minute."""
    base = {'us-east': 5200, 'eu-west': 3900, 'ap-south': 2300}[region]
    return round(base * (1 + 0.25 * math.sin(minute / 17)) + 900 * noise(region, minute))


def window_total(region: str, start: int) -> int:
    """Requests that region served in the five-minute window starting at
    start (epoch seconds): the sum of its minutes, so the bars and the
    detail chart always agree."""
    first = start // 60
    return sum(requests_per_minute(region, m) for m in range(first, first + WINDOW // 60))


def fast_clock(speed: float, real: Callable[[], float] = time.time) -> Callable[[], float]:
    """A clock in epoch seconds that starts at real() and runs speed times
    faster than it, so windows slide sooner when you are watching."""
    start = real()
    return lambda: start + (real() - start) * speed
