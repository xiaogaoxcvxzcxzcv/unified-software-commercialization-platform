import type { ClientBlockState } from "../../../contracts/src/index.js";

export type EntitlementBlockState = ClientBlockState;

export interface EntitlementBlockError {
  readonly code: string;
  readonly message: string;
  readonly retryable: boolean;
}

export interface EntitlementBlockCommonProps {
  readonly state: EntitlementBlockState;
  readonly error?: EntitlementBlockError;
  readonly successMessage?: string;
  readonly emptyMessage?: string;
  readonly disabledMessage?: string;
  readonly onRetry?: () => void;
  readonly className?: string;
}

export interface EntitlementFeatureItem {
  readonly code: string;
  readonly label?: string;
  readonly kind?: string;
  readonly value?: string | number | boolean | null;
}

export interface EntitlementSummaryValue {
  readonly revision: number;
  readonly planCode: string | null;
  readonly validUntil: string | null;
  readonly offlineGraceUntil: string | null;
  readonly updatedAt: string;
  readonly features: readonly EntitlementFeatureItem[];
  readonly emptyReason?: "never_owned" | "expired";
}

export interface EntitlementSummaryActions {
  readonly renew?: () => void;
  readonly upgrade?: () => void;
  readonly retry?: () => void;
}
