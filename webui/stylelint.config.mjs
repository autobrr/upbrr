export default {
  extends: ["stylelint-config-recommended"],
  ignoreFiles: ["dist/**"],
  rules: {
    "at-rule-no-unknown": [
      true,
      {
        ignoreAtRules: ["tailwind"],
      },
    ],
  },
};
