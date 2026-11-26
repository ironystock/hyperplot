# hp_wire.py: the wire helpers of spec/spec.md, Appendix B, copied verbatim.
#
# Everything below the first blank line is the spec's Python block, byte for
# byte (test_wire.py checks it), so a fix to the spec reaches this file by
# copying the block again. The helpers write the value forms of spec §1.1,
# escape text for spec §3.6, write the hp-tips JSON of spec §3.5 and split
# SSE messages for spec §4.5. Standard library only.

import json
import math
import re
from datetime import datetime, timedelta, timezone
from decimal import Decimal


def number(v: float) -> str:
    """A finite float written like ECMAScript's Number.prototype.toString."""
    if not math.isfinite(v):
        raise ValueError(v)
    if v == 0:
        return '0'
    sign = '-' if v < 0 else ''
    t = Decimal(repr(abs(v))).normalize().as_tuple()  # shortest round-trip digits
    d = ''.join(map(str, t.digits))
    k, n = len(d), len(d) + t.exponent  # the value is 0.d × 10**n
    if k <= n <= 21:
        return sign + d + '0' * (n - k)
    if 0 < n <= 21:
        return sign + d[:n] + '.' + d[n:]
    if -6 < n <= 0:
        return sign + '0.' + '0' * -n + d
    e = n - 1
    return sign + d[0] + ('.' + d[1:] if k > 1 else '') + 'e' + ('+' if e > 0 else '-') + str(abs(e))


def time_value(t: datetime) -> str:
    """RFC 3339 in UTC; a fraction only when non-zero, without trailing zeros."""
    if t.utcoffset() is None:
        raise ValueError('naive datetime: attach its time zone first')
    t = t.astimezone(timezone.utc)
    s = t.strftime('%Y-%m-%dT%H:%M:%S')
    if t.microsecond:
        s += ('.%06d' % t.microsecond).rstrip('0')
    return s + 'Z'


_EPOCH = datetime(1970, 1, 1, tzinfo=timezone.utc)


def epoch_ms(t: datetime) -> str:
    """Milliseconds since the epoch, from integer microseconds."""
    if t.utcoffset() is None:
        raise ValueError('naive datetime: attach its time zone first')
    d = t - _EPOCH
    return number(((d.days * 86400 + d.seconds) * 1_000_000 + d.microseconds) / 1000)


_TIME = re.compile(r'([0-9]{4})-([0-9]{2})-([0-9]{2})[Tt]([0-9]{2}):([0-9]{2})'
                   r'(?::([0-9]{2})(?:\.([0-9]+))?)?([Zz]|[+-][0-9]{2}:[0-9]{2})?')


def parse_time_value(s: str, tz=timezone.utc) -> datetime:
    """An RFC 3339 date-time (any offset, any number of fraction digits) or the
    HTML datetime-local form (no offset, seconds optional), which is read in
    tz. Returns an aware UTC datetime; digits past microseconds are dropped.
    Works on Python 3.8+ (fromisoformat rejects 'Z' before 3.11)."""
    m = _TIME.fullmatch(s)
    if not m or (m[8] and not m[6]):  # an offset needs seconds (RFC 3339)
        raise ValueError(s)
    y, mo, d, h, mi, sec, frac, off = m.groups()
    t = datetime(int(y), int(mo), int(d), int(h), int(mi), int(sec or 0), int((frac or '0')[:6].ljust(6, '0')))
    if off is None:
        t = t.replace(tzinfo=tz)
    elif off in 'Zz':
        t = t.replace(tzinfo=timezone.utc)
    else:
        if int(off[4:6]) > 59:  # RFC 3339: offset minutes run to 59
            raise ValueError(s)
        sign = 1 if off[0] == '+' else -1
        t = t.replace(tzinfo=timezone(sign * timedelta(hours=int(off[1:3]), minutes=int(off[4:6]))))
    try:
        return t.astimezone(timezone.utc)
    except OverflowError:  # outside years 1 to 9999 once moved to UTC
        raise ValueError(s) from None


_NUMBER = re.compile(r'-?(?:0|[1-9][0-9]*)(?:\.[0-9]+)?(?:[eE][+-]?[0-9]+)?')


def parse_number(s: str) -> float:
    """A finite JSON number. float() alone also takes 'inf', 'nan', '1_0'
    and surrounding whitespace."""
    if not _NUMBER.fullmatch(s):
        raise ValueError(s)
    v = float(s)
    if not math.isfinite(v):  # 1e999
        raise ValueError(s)
    return v


def _xml_chars(s: str) -> str:
    """Drop what XML 1.0 cannot carry: C0 controls other than TAB, LF and CR,
    lone surrogates, U+FFFE and U+FFFF. (Decode bytes with errors='replace'
    first, so invalid UTF-8 becomes U+FFFD.)"""
    return ''.join(c for c in s if (o := ord(c)) in (9, 10, 13) or 0x20 <= o <= 0xD7FF
                   or 0xE000 <= o <= 0xFFFD or o >= 0x10000)


def xml_text(s: str) -> str:
    """Element text. HTML escapers (Django's escape, Jinja's autoescape) do
    not drop control characters, so they are not enough."""
    s = _xml_chars(s).replace('&', '&amp;').replace('<', '&lt;').replace('>', '&gt;')
    return s.replace('\r', '&#13;')


def xml_attr(s: str) -> str:
    """A double-quoted attribute value."""
    return xml_text(s).replace('"', '&quot;').replace('\t', '&#9;').replace('\n', '&#10;')


def wire_text(s: str) -> str:
    """s with each lone surrogate replaced by U+FFFD (§3.5). Python strings
    can hold them (json.loads of an escaped surrogate, or bytes decoded with
    errors='surrogateescape'), and str.encode() raises on them. Apply it to
    every string you hash into a key; tips_json applies it itself."""
    return ''.join(chr(0xFFFD) if 0xD800 <= ord(c) <= 0xDFFF else c for c in s)


def tips_json(payload: dict) -> str:
    """The hp-tips text: compact JSON with <, > and & escaped (plus U+FFFE/F),
    and lone surrogates replaced by U+FFFD."""
    s = wire_text(json.dumps(payload, ensure_ascii=False, separators=(',', ':'), allow_nan=False))
    for c in '<>&' + chr(0xFFFE) + chr(0xFFFF):
        s = s.replace(c, '\\u%04x' % ord(c))
    return s


def sse_message(fragment: str) -> str:
    """One unnamed SSE message: a data: line per line of the fragment, split
    on CRLF, CR and LF as hx-sse splits them."""
    return ''.join('data: ' + line + '\n' for line in re.split(r'\r\n|\r|\n', fragment)) + '\n'
