export default {
  extends: ["@commitlint/config-conventional"],
  rules: {
    // 115 = 95th percentile of main's subject lengths (88 chars) + ~30% headroom.
    // Leaves room for detail without encouraging sprawl; re-derive if history shifts.
    "header-max-length": [2, "always", 115],

    // scope-case off: history uses uppercase tracker acronyms like fix(BT), fix(ASC).
    "scope-case": [0],

    // No multi-line bodies exist in history yet, so no empirical base — reuse the
    // header budget. Kept at warning level (1) during first rollout so it nudges
    // rather than blocks; tighten to error once history shows the real shape.
    "body-max-line-length": [1, "always", 115],
    "footer-max-line-length": [1, "always", 115],
  },
};
