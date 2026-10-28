export class ApiError extends Error {
  constructor(
    public status: number,
    message: string,
    public code?: string,
    /** The error body's other fields, for errors that carry more than a code. */
    public details?: Record<string, unknown>,
  ) {
    super(message)
    this.name = 'ApiError'
  }
}
