export class PostGripAgentError extends Error {
  constructor(message: string, options?: ErrorOptions) {
    super(message, options);
    this.name = 'PostGripAgentError';
  }
}

export class ApplicationFailure extends PostGripAgentError {
  readonly type: string;
  readonly nonRetryable: boolean;
  readonly details: unknown[];

  private constructor(message: string, options: {
    type?: string;
    nonRetryable?: boolean;
    details?: unknown[];
    cause?: unknown;
  } = {}) {
    super(message, { cause: options.cause });
    this.name = 'ApplicationFailure';
    this.type = options.type ?? 'ApplicationFailure';
    this.nonRetryable = options.nonRetryable ?? false;
    this.details = options.details ?? [];
  }

  static create(options: {
    message: string;
    type?: string;
    nonRetryable?: boolean;
    details?: unknown[];
    cause?: unknown;
  }): ApplicationFailure {
    return new ApplicationFailure(options.message, options);
  }

  static nonRetryable(message: string, type = 'NonRetryableFailure', ...details: unknown[]): ApplicationFailure {
    return new ApplicationFailure(message, { type, nonRetryable: true, details });
  }
}

export class TimeoutFailure extends PostGripAgentError {
  constructor(message = 'operation timed out') {
    super(message);
    this.name = 'TimeoutFailure';
  }
}

export class CancelledFailure extends PostGripAgentError {
  constructor(message = 'operation cancelled') {
    super(message);
    this.name = 'CancelledFailure';
  }
}

export class TaskFailedError extends PostGripAgentError {
  readonly taskId: string;

  constructor(taskId: string, message: string) {
    super(message);
    this.name = 'TaskFailedError';
    this.taskId = taskId;
  }
}
