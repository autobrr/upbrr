# upbrr

upbrr prepares releases for tracker submission while preserving the source content's identity and the operator's upload authority.

## Duplicate checking

**Work**:
A movie, series, or other provider-level title shared by every release made from that title.
_Avoid_: Release, duplicate

**Content scope**:
The part of a work carried by a release, such as one movie, episode, episode range, season pack, or complete series.
_Avoid_: Slot, category

**Season pack**:
A release containing a season's episodes. It overlaps every episode in that season and is preferred over each individual episode.
_Avoid_: Separate slot

**Release variant**:
One technical presentation of a content scope, distinguished by facts such as disc/remux/encode form, resolution, source, HDR, edition, region, or 3D presentation.
_Avoid_: Work, content

**Slot**:
A tracker-defined set of release variants that compete under the same duplicate or trumping rule.
_Avoid_: Work, format

**Potential duplicate**:
A release variant for the same overlapping content scope that has not been proven to coexist in another slot.
_Avoid_: Exact duplicate

**Exact duplicate**:
A release whose release name, any tracker-returned filename, or complete file identity exactly matches the proposed release strongly enough to block without slot inference.
_Avoid_: Potential duplicate

**Coexisting release**:
A release variant proven by general or tracker-specific rules to occupy a distinct permitted slot.
_Avoid_: Non-match
