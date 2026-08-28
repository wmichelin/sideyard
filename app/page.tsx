import {
  SAMPLE_MEMBERS,
  formatCurrency,
  getSummary,
  type MemberStatus,
} from "@/lib/members";
import styles from "./page.module.css";

const STATUS_LABEL: Record<MemberStatus, string> = {
  paid: "Paid",
  partial: "Partial",
  overdue: "Overdue",
};

function StatusBadge({ status }: { status: MemberStatus }) {
  return (
    <span className={`${styles.badge} ${styles[status]}`}>
      {STATUS_LABEL[status]}
    </span>
  );
}

export default function Home() {
  const { totalExpected, totalCollected, paidCount, overdueCount } =
    getSummary(SAMPLE_MEMBERS);
  const outstanding = totalExpected - totalCollected;
  const collectionRate = Math.round((totalCollected / totalExpected) * 100);

  return (
    <div className={styles.page}>
      <header className={styles.header}>
        <div>
          <p className={styles.eyebrow}>Spring 2026</p>
          <h1 className={styles.title}>ClubDue</h1>
          <p className={styles.subtitle}>
            Membership dues dashboard — sample data to get you started.
          </p>
        </div>
        <button className={styles.primaryBtn} type="button">
          + Add member
        </button>
      </header>

      <section className={styles.stats}>
        <div className={styles.statCard}>
          <span className={styles.statLabel}>Collected</span>
          <span className={styles.statValue}>
            {formatCurrency(totalCollected)}
          </span>
          <span className={styles.statMeta}>
            of {formatCurrency(totalExpected)} expected
          </span>
        </div>
        <div className={styles.statCard}>
          <span className={styles.statLabel}>Outstanding</span>
          <span className={`${styles.statValue} ${styles.outstanding}`}>
            {formatCurrency(outstanding)}
          </span>
          <span className={styles.statMeta}>{collectionRate}% collected</span>
        </div>
        <div className={styles.statCard}>
          <span className={styles.statLabel}>Members paid</span>
          <span className={styles.statValue}>{paidCount}</span>
          <span className={styles.statMeta}>
            {overdueCount} overdue
          </span>
        </div>
      </section>

      <section className={styles.tableSection}>
        <h2 className={styles.sectionTitle}>Members</h2>
        <div className={styles.tableWrap}>
          <table className={styles.table}>
            <thead>
              <tr>
                <th>Name</th>
                <th>Email</th>
                <th>Due</th>
                <th>Paid</th>
                <th>Balance</th>
                <th>Status</th>
              </tr>
            </thead>
            <tbody>
              {SAMPLE_MEMBERS.map((member) => {
                const balance = member.duesAmount - member.amountPaid;
                return (
                  <tr key={member.id}>
                    <td className={styles.nameCell}>{member.name}</td>
                    <td className={styles.muted}>{member.email}</td>
                    <td>{formatCurrency(member.duesAmount)}</td>
                    <td>{formatCurrency(member.amountPaid)}</td>
                    <td className={balance > 0 ? styles.balanceDue : ""}>
                      {formatCurrency(balance)}
                    </td>
                    <td>
                      <StatusBadge status={member.status} />
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
      </section>
    </div>
  );
}
