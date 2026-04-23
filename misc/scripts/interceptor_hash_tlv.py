"""MT Interceptor — apply per-user TLV profiles from a CSV file.

File:   /etc/jasmin/resource/tlv_profiles.csv
Row:    username ; TAG:VALUE[:TYPE] ; TAG:VALUE[:TYPE] ; ...
Types:  Int1 Int2 Int4 Int8 OctetString COctetString

Behaviour
---------
On every outbound submit this script:

1. Looks up ``routable.user.username`` in the CSV file (O(1) dict access).
2. For each TLV declared in that user's profile:
     * PDU already carries the tag with the same value  -> keep as is
     * PDU carries the tag with a different value       -> replace, record the mismatch
     * PDU does not carry the tag                       -> add it
3. Any TLV the caller sent that is NOT in the profile is passed through untouched.

The profile is authoritative: its values always win over the caller's for any
tag it declares. Ops can see what happened via `extra` keys:
  * `tlv_profile`   — `hit:<user>` or `miss:<user>`
  * `tlv_added`     — comma-list of tags injected from the profile
  * `tlv_mismatch`  — semi-list of tags where caller & profile disagreed (profile won)
  * `tlv_kept`      — comma-list of tags where caller & profile already matched

Caching
-------
The parsed profile dict is cached in the interceptor engine's shared `cache`
dict (see `jasmin/interceptor/interceptor.py`). A new parse happens only when
the file's mtime changes, guarded by `cache_lock` (an RLock). Parsing runs
outside the lock so concurrent first-time callers don't block on I/O.

Pre-injected namespace (no `import` needed):
  routable, smpp_status, http_status, extra,
  hashlib, re, json, datetime, math, struct, os, cache, cache_lock
"""

PROFILE_PATH = '/etc/jasmin/resource/tlv_profiles.csv'
CACHE_KEY    = 'tlv_profiles'
VALID_TYPES  = ('Int1', 'Int2', 'Int4', 'Int8', 'OctetString', 'COctetString')


def _parse_tlv(spec):
    """Parse `TAG:VALUE[:TYPE]` (or `TAG:TYPE:VALUE`) into `(tag, value, type_or_None)`.

    Type detection: the first colon-separated part after the tag that matches
    one of VALID_TYPES is taken as the type; all remaining parts (joined by
    ``:``) form the value. This makes the parser tolerant of both orderings
    without needing a separate flag.
    """
    if not spec or ':' not in spec:
        return None
    parts = spec.split(':')
    tag_str = parts[0].strip()
    try:
        tag = int(tag_str, 16) if tag_str.lower().startswith('0x') else int(tag_str)
    except ValueError:
        return None
    tlv_type = None
    value_parts = []
    for p in parts[1:]:
        p_stripped = p.strip()
        if tlv_type is None and p_stripped in VALID_TYPES:
            tlv_type = p_stripped
        else:
            value_parts.append(p)
    if not value_parts:
        return None
    return (tag, ':'.join(value_parts).strip(), tlv_type)


def _parse_profiles(path):
    """Read the CSV and return ``{username: [(tag, value, type_or_None), ...]}``.

    Blank lines and lines beginning with ``#`` are ignored. Unparseable TLV
    specs within a row are silently skipped (the rest of the row still loads).
    """
    profiles = {}
    with open(path, 'r', encoding='utf-8') as fh:
        for line in fh:
            line = line.strip()
            if not line or line.startswith('#'):
                continue
            cols = [c.strip() for c in line.split(';')]
            if len(cols) < 2 or not cols[0]:
                continue
            username = cols[0]
            tlvs = []
            for spec in cols[1:]:
                parsed = _parse_tlv(spec)
                if parsed is not None:
                    tlvs.append(parsed)
            profiles[username] = tlvs
    return profiles


def _get_profiles(path):
    """Return the cached profiles dict, reloading only on mtime change."""
    try:
        mtime = os.path.getmtime(path)
    except OSError:
        return {}
    with cache_lock:
        entry = cache.get(CACHE_KEY)
        if entry and entry.get('path') == path and entry.get('mtime') == mtime:
            return entry['data']
    # cache miss or stale — parse outside the lock so concurrent first-time
    # callers don't serialise on disk I/O
    try:
        data = _parse_profiles(path)
    except OSError:
        data = {}
    with cache_lock:
        # re-check: another caller may have populated the cache while we parsed
        existing = cache.get(CACHE_KEY)
        if existing and existing.get('mtime') == mtime:
            return existing['data']
        cache[CACHE_KEY] = {'path': path, 'mtime': mtime, 'data': data}
        return data


# ---- apply profile to this submit -----------------------------------------

username = getattr(routable.user, 'username', '') or ''
if isinstance(username, bytes):
    username = username.decode('utf-8', errors='replace')

profile = _get_profiles(PROFILE_PATH).get(username)

if not profile:
    extra['tlv_profile'] = 'miss:%s' % username
else:
    pdu_tlvs = list(getattr(routable.pdu, 'custom_tlvs', []) or [])
    index_by_tag = {}
    for i, t in enumerate(pdu_tlvs):
        if t and len(t) >= 1:
            index_by_tag[int(t[0]) & 0xFFFF] = i

    mismatches = []
    added = []
    kept = []

    for tag, profile_value, profile_type in profile:
        new_tuple = (tag, None, profile_type, profile_value.encode('utf-8'))
        if tag in index_by_tag:
            existing = pdu_tlvs[index_by_tag[tag]]
            existing_value = existing[3] if len(existing) >= 4 else None
            if isinstance(existing_value, bytes):
                existing_str = existing_value.decode('utf-8', errors='replace')
            else:
                existing_str = str(existing_value) if existing_value is not None else ''
            if existing_str == profile_value:
                kept.append(tag)
            else:
                mismatches.append(
                    '0x%04X:caller=%r/profile=%r'
                    % (tag, existing_str[:40], profile_value[:40]))
                pdu_tlvs[index_by_tag[tag]] = new_tuple
        else:
            pdu_tlvs.append(new_tuple)
            added.append(tag)

    routable.pdu.custom_tlvs = pdu_tlvs

    extra['tlv_profile'] = 'hit:%s' % username
    if added:
        extra['tlv_added'] = ','.join('0x%04X' % t for t in added)
    if mismatches:
        extra['tlv_mismatch'] = ';'.join(mismatches)
    if kept:
        extra['tlv_kept'] = ','.join('0x%04X' % t for t in kept)
