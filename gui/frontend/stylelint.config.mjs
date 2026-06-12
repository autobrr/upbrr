export default {
  extends: ["stylelint-config-recommended"],
  ignoreFiles: ["dist/**", "src/wailsjs/**"],
  rules: {
    "at-rule-no-unknown": [
      true,
      {
        ignoreAtRules: ["tailwind"],
      },
    ],
  },
};
