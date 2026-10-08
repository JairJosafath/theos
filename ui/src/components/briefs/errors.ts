// The message of a write the API refused, as the Brief forms show it.

export function errorText(err: unknown): string {
  return err instanceof Error ? err.message : String(err)
}
