export type MemberStatus = "paid" | "partial" | "overdue";

export interface Member {
  id: string;
  name: string;
  email: string;
  duesAmount: number;
  amountPaid: number;
  dueDate: string;
  status: MemberStatus;
}

export const SAMPLE_MEMBERS: Member[] = [
  {
    id: "1",
    name: "Alex Rivera",
    email: "alex@example.com",
    duesAmount: 120,
    amountPaid: 120,
    dueDate: "2026-01-15",
    status: "paid",
  },
  {
    id: "2",
    name: "Jordan Lee",
    email: "jordan@example.com",
    duesAmount: 120,
    amountPaid: 60,
    dueDate: "2026-01-15",
    status: "partial",
  },
  {
    id: "3",
    name: "Sam Chen",
    email: "sam@example.com",
    duesAmount: 120,
    amountPaid: 0,
    dueDate: "2026-01-15",
    status: "overdue",
  },
  {
    id: "4",
    name: "Taylor Brooks",
    email: "taylor@example.com",
    duesAmount: 120,
    amountPaid: 120,
    dueDate: "2026-01-15",
    status: "paid",
  },
  {
    id: "5",
    name: "Morgan Walsh",
    email: "morgan@example.com",
    duesAmount: 120,
    amountPaid: 0,
    dueDate: "2026-01-15",
    status: "overdue",
  },
];

export function formatCurrency(amount: number): string {
  return new Intl.NumberFormat("en-US", {
    style: "currency",
    currency: "USD",
  }).format(amount);
}

export function getSummary(members: Member[]) {
  const totalExpected = members.reduce((sum, m) => sum + m.duesAmount, 0);
  const totalCollected = members.reduce((sum, m) => sum + m.amountPaid, 0);
  const paidCount = members.filter((m) => m.status === "paid").length;
  const overdueCount = members.filter((m) => m.status === "overdue").length;

  return { totalExpected, totalCollected, paidCount, overdueCount };
}
