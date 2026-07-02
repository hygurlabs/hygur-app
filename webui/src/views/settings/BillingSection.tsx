import { useQuery } from "@tanstack/react-query";
import { api } from "../../lib/api";
import { fmtDate } from "../../lib/format";
import { Row, Section } from "./common";

/** Billing panel (cloud customers). Reads the subscription status from the control
 *  plane and links to the Stripe customer portal. Self-hides when there's no
 *  billing account (the operator's hand-provisioned instance, self-host, or a
 *  browser with no device token) — its query just errors and the section vanishes. */
export function BillingSection() {
  const q = useQuery({
    queryKey: ["billing-status"],
    queryFn: () => api.billingStatus(),
    retry: false,
  });
  if (q.isError || !q.data) return null;
  const b = q.data;
  const label =
    b.status === "active"
      ? "Active"
      : b.status === "trialing"
        ? "Trial"
        : b.status === "past_due"
          ? "Payment due"
          : b.status === "canceled"
            ? "Canceled"
            : b.status;
  const until = b.valid_until
    ? ` · ${b.active ? "renews" : "ends"} ${fmtDate(b.valid_until)}`
    : "";
  return (
    <Section title="Billing">
      <Row label="Plan" hint={`Hygur Cloud — Personal${until}`}>
        <span className={`text-[13px] font-medium ${b.active ? "text-success" : "text-danger"}`}>
          {label}
        </span>
      </Row>
      {b.portal_url && (
        <Row label="Subscription" hint="Update payment method, download invoices, or cancel.">
          <a
            href={b.portal_url}
            target="_blank"
            rel="noopener noreferrer"
            className="rounded-md bg-accent px-3 py-1.5 text-[13px] font-medium text-white transition-opacity hover:opacity-90"
          >
            Manage
          </a>
        </Row>
      )}
    </Section>
  );
}
