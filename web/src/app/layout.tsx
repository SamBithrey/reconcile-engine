import type { Metadata } from "next";
import type { ReactNode } from "react";

import { AppHeader } from "@/components/AppHeader";
import "./globals.css";

export const metadata: Metadata = {
  title: "Reconciliation Queue | Reconcile Engine",
  description:
    "Deterministic 3-way PO/GRN/Invoice reconciliation and Human-in-the-Loop verification dashboard",
};

export default function RootLayout({ children }: { children: ReactNode }) {
  return (
    <html lang="en">
      <body className="min-h-screen bg-slate-50 font-sans text-slate-900">
        <a href="#main-content" className="skip-link visually-hidden">
          Skip to main content
        </a>
        <AppHeader />
        <main
          id="main-content"
          tabIndex={-1}
          className="mx-auto max-w-[1440px] px-4 py-6 sm:px-6 lg:px-8"
        >
          {children}
        </main>
      </body>
    </html>
  );
}
