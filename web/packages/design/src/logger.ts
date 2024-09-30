/*
 * Teleport
 * Copyright (C) 2023  Gravitational, Inc.
 *
 * This program is free software: you can redistribute it and/or modify
 * it under the terms of the GNU Affero General Public License as published by
 * the Free Software Foundation, either version 3 of the License, or
 * (at your option) any later version.
 *
 * This program is distributed in the hope that it will be useful,
 * but WITHOUT ANY WARRANTY; without even the implied warranty of
 * MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
 * GNU Affero General Public License for more details.
 *
 * You should have received a copy of the GNU Affero General Public License
 * along with this program.  If not, see <http://www.gnu.org/licenses/>.
 */

export class Logger {
  constructor(private name = 'default') {}

  log(
    level: 'trace' | 'warn' | 'info' | 'log' | 'debug' | 'error' = 'log',
    ...args: any[]
  ) {
    window.console[level](`%c[${this.name}]`, `color: blue;`, ...args);
  }

  trace(...args: any[]) {
    this.log('trace', ...args);
  }

  warn(...args: any[]) {
    this.log('warn', ...args);
  }

  info(...args: any[]) {
    this.log('info', ...args);
  }

  debug(...args: any[]) {
    this.log('debug', ...args);
  }

  error(...args: any[]) {
    this.log('error', ...args);
  }
}

export default {
  create: (...args: any[]) => new Logger(...args),
};
