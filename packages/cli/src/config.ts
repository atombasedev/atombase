import { existsSync } from "node:fs";
import { resolve } from "node:path";
import { createJiti } from "jiti";

export interface AtomicbaseConfig {
  url?: string;
  apiKey?: string;
  schemas: string;
  insecure?: boolean;
}

const DEFAULT_CONFIG: Required<AtomicbaseConfig> = {
  url: "http://localhost:8080",
  apiKey: "",
  schemas: "./definitions",
  insecure: false,
};

const CONFIG_FILES = [
  "atomicbase.config.ts",
  "atomicbase.config.js",
  "atomicbase.config.mjs",
];

// Create jiti instance for loading TypeScript config files
const jiti = createJiti(import.meta.url);

// Get the user's actual working directory
// INIT_CWD is set by npm/npx to the original directory where the command was run
const workingDir = process.env.INIT_CWD || process.cwd();

/**
 * Load configuration from file and environment variables.
 * Priority: env vars > config file > defaults
 */
export async function loadConfig(): Promise<Required<AtomicbaseConfig>> {
  let fileConfig: Partial<AtomicbaseConfig> = {};

  // Try to load config file using jiti (handles TypeScript natively)
  for (const filename of CONFIG_FILES) {
    const configPath = resolve(workingDir, filename);
    if (existsSync(configPath)) {
      try {
        const module = await jiti.import(configPath);
        fileConfig = (module as { default?: AtomicbaseConfig }).default ?? module as AtomicbaseConfig;
        break;
      } catch (err) {
        console.error(`Error loading ${filename}:`, err);
      }
    }
  }

  // Merge: defaults < file config < env vars
  const insecureEnv = process.env.ATOMICBASE_INSECURE;
  const insecure = insecureEnv !== undefined
    ? insecureEnv === "1" || insecureEnv.toLowerCase() === "true"
    : fileConfig.insecure ?? DEFAULT_CONFIG.insecure;

  return {
    url: process.env.ATOMICBASE_URL ?? fileConfig.url ?? DEFAULT_CONFIG.url,
    apiKey: process.env.ATOMICBASE_API_KEY ?? fileConfig.apiKey ?? DEFAULT_CONFIG.apiKey,
    schemas: fileConfig.schemas ?? DEFAULT_CONFIG.schemas,
    insecure,
  };
}

/**
 * Helper to define config with type checking.
 * Used in atomicbase.config.ts files.
 */
export function defineConfig(config: Partial<AtomicbaseConfig>): AtomicbaseConfig {
  return {
    ...DEFAULT_CONFIG,
    ...config,
  };
}
