import type { ClientBlockState } from "../../../contracts/src/index.js";

export type AccountBlockState = ClientBlockState;

export interface AccountBlockError {
  readonly code: string;
  readonly message: string;
  readonly retryable: boolean;
}

export interface AccountBlockCommonProps {
  readonly state: AccountBlockState;
  readonly error?: AccountBlockError;
  readonly successMessage?: string;
  readonly emptyMessage?: string;
  readonly disabledMessage?: string;
  readonly onRetry?: () => void;
  readonly className?: string;
}

export interface AccountProviderOption {
  readonly id: string;
  readonly label: string;
}

export interface AccountUserSummary {
  readonly displayName: string;
  readonly maskedIdentifier?: string;
  readonly avatarUrl?: string;
}

export interface AccountProfileValue {
  readonly displayName: string;
  readonly locale: string;
  readonly timezone: string;
  readonly avatarUrl?: string;
  readonly version: number;
}

export interface AccountSessionSummary {
  readonly id: string;
  readonly deviceLabel: string;
  readonly authenticationMethod: string;
  readonly applicationLabel: string;
  readonly environmentLabel?: string;
  readonly lastSeenLabel: string;
  readonly expiresLabel: string;
  readonly current: boolean;
  readonly revoked: boolean;
}

export interface AccountExternalIdentity {
  readonly id: string;
  readonly providerLabel: string;
  readonly subjectMasked: string;
  readonly unlinkAllowed: boolean;
}

export interface AccountSecuritySummary {
  readonly passwordConfigured: boolean;
  readonly activeSessionCount: number;
  readonly externalIdentityCount: number;
}
