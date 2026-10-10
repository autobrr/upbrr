"""Hash normalized evidence collected through Audionut, without network access.

The input must use collect()'s exact normalized schema. A hash is a freshness
check, not authorization; the publisher recollects and compares the evidence.
"""

import json
import sys

from bridge import MAX_SNAPSHOT, REPOSITORY, Rejected, digest, require, unique_object


def hash_snapshot(raw):
    require(len(raw) <= MAX_SNAPSHOT, "snapshot too large")
    source = json.loads(raw, object_pairs_hook=unique_object)
    require(type(source) is dict and source.get("repository") == REPOSITORY, "wrong repository")
    require(source.get("kind") in ("issue", "pull_request", "discussion"), "wrong item kind")
    return {"head": source["head"], "source_hash": digest(source)}


if __name__ == "__main__":
    try:
        require(len(sys.argv) == 2, "provide one local normalized snapshot file")
        with open(sys.argv[1], "rb") as handle:
            raw = handle.read(MAX_SNAPSHOT + 1)
        print(json.dumps(hash_snapshot(raw)))
    except (Rejected, KeyError, TypeError, ValueError, OSError, RecursionError):
        print("Invalid normalized snapshot", file=sys.stderr)
        sys.exit(1)
