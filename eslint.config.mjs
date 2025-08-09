import { globalIgnores } from "eslint/config";
import js from "@eslint/js";
import ts from "typescript-eslint";
import globals from "globals";
import stylistic from "@stylistic/eslint-plugin";

export default ts.config([
  globalIgnores(["dist/**/*.js"]),
  js.configs.recommended,
  ts.configs.strict,
  ts.configs.recommended,
  ts.configs.stylistic,
  {
    files: ["src/**/*.ts"],
    languageOptions: {
      ecmaVersion: "latest",
      sourceType: "module",
      globals: globals.node,
    },

    rules: {
      "no-empty": [
        "error",
        {
          allowEmptyCatch: true,
        },
      ],
      "@typescript-eslint/explicit-function-return-type": "warn",
      "@typescript-eslint/no-unused-vars": [
        "error",
        {
          caughtErrorsIgnorePattern: "^_+$",
        },
      ],
    },
  },
  {
    files: ["public/**/*.js"],
    languageOptions: {
      ecmaVersion: "latest",
      sourceType: "module",
      globals: {
        ...globals.browser,
        ...globals.es2026,
      },
    },
  },
  stylistic.configs.customize({
    indent: 2,
    semi: true,
    quotes: "double",
    severity: "error",
    braceStyle: "1tbs",
  }),
  {
    rules: {
      "@stylistic/operator-linebreak": ["error", "after"],
      "@stylistic/arrow-parens": ["error", "always"],
    },
  },
]);
