// Тонкая обёртка над сгенерированными Wails-биндингами
// (frontend/bindings — DO NOT EDIT). Типы моделей — из сгенерированного
// models.ts; null-возвраты биндингов (Go-nil) скрываем за обычными типами.

import * as app from '../../bindings/ender-space/internal/app/app';
import type { FileMeta, PrinterStatus, Storage } from '../../bindings/ender-space/internal/printer/models';

export type { FileMeta, PrinterStatus, Storage };

export const api = {
  connect: (ip: string): Promise<void> => app.Connect(ip),
  disconnect: (): Promise<void> => app.Disconnect(),
  status: (): Promise<PrinterStatus> => app.Status(),
  storage: (): Promise<Storage> => app.Storage(),
  // Go отдаёт nil, если список не получился, — для UI это пустой список.
  listFiles: async (): Promise<FileMeta[]> => (await app.ListFiles()) ?? [],
  deleteFiles: (paths: string[]): Promise<void> => app.DeleteFiles(paths),
};
