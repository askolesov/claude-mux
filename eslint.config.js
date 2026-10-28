// ESLint for the page (internal/hub/web/app.js), a plain script loaded
// after xterm.js and its addons from vendor/.
import js from "@eslint/js";
import globals from "globals";

export default [
  { ignores: ["internal/hub/web/vendor/**", "docs/**"] },
  js.configs.recommended,
  {
    files: ["internal/hub/web/**/*.js"],
    languageOptions: {
      ecmaVersion: 2024,
      sourceType: "script",
      globals: { ...globals.browser, Terminal: "readonly", FitAddon: "readonly", WebLinksAddon: "readonly" },
    },
    rules: {
      eqeqeq: "error",
      "no-var": "error",
      "prefer-const": "error",
      "no-unused-vars": ["error", { args: "none", caughtErrors: "none" }],
      "no-shadow": "error",
      "no-implicit-globals": "off",
    },
  },
  {
    files: ["eslint.config.js"],
    languageOptions: { globals: globals.node },
  },
];
