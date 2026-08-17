import { existsSync, readFileSync } from 'node:fs';
import { dirname, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

export function loadExampleEnv(moduleUrl: string): void {
  for (const path of [
    resolve(process.cwd(), '.env'),
    resolve(dirname(fileURLToPath(moduleUrl)), '.env'),
  ]) {
    loadDotenvFile(path);
  }
}

function loadDotenvFile(path: string): void {
  if (!existsSync(path)) return;

  for (const rawLine of readFileSync(path, 'utf8').split(/\r?\n/)) {
    let line = rawLine.trim();
    if (!line || line.startsWith('#')) continue;
    if (line.startsWith('export ')) line = line.slice('export '.length);
    const equals = line.indexOf('=');
    if (equals < 0) continue;

    const key = line.slice(0, equals).trim();
    if (!key || process.env[key]) continue;
    process.env[key] = parseDotenvValue(line.slice(equals + 1));
  }
}

function parseDotenvValue(value: string): string {
  value = value.trim();
  if (value.length >= 2) {
    const first = value[0];
    const last = value[value.length - 1];
    if ((first === '"' && last === '"') || (first === "'" && last === "'")) {
      return value.slice(1, -1);
    }
  }
  return value;
}
