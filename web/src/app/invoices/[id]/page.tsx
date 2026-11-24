import type { Metadata } from "next";

import { InvoiceDetailClient } from "@/components/InvoiceDetailClient";

export async function generateMetadata({
  params,
}: {
  params: Promise<{ id: string }>;
}): Promise<Metadata> {
  const { id } = await params;
  return {
    title: `Invoice ${id.slice(0, 8)} | Reconcile Engine`,
  };
}

export default async function InvoiceDetailPage({
  params,
}: {
  params: Promise<{ id: string }>;
}) {
  const { id } = await params;
  return <InvoiceDetailClient invoiceId={id} />;
}
