import type { Metadata } from "next";
import "./globals.css";

export const metadata: Metadata = {
  title: "ClubDue — Membership Dues",
  description: "Track club membership dues and payments",
};

export default function RootLayout({
  children,
}: {
  children: React.ReactNode;
}) {
  return (
    <html lang="en">
      <body>{children}</body>
    </html>
  );
}
