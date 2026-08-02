import { Empty } from "antd";
import { DownOutlined, RightOutlined } from "@ant-design/icons";

import type { Analytics, AnalyticsRow } from "./types";

/**
 * The delivery analytics matrix.
 *
 * Two tables, not one, because only the pull side has a queryable history: the
 * audit table can be windowed, the delivery counters are cumulative totals
 * since the gateway started. Merging them would put a since-boot number under a
 * "last 15m" heading — a lie the layout tells on its own.
 */

const number = new Intl.NumberFormat(undefined, {
  notation: "compact",
  maximumFractionDigits: 1,
});

type PanelProps = {
  analytics?: Analytics;
  open: boolean;
  onToggle: () => void;
  onSelect: (nodeID: string) => void;
  selectedId: string | null;
};

const StatusCell = ({ row }: { row: AnalyticsRow }) => (
  <span className="analytics-status" title={row.reason}>
    <span className={`topology-dot is-${row.status}`} aria-hidden="true" />
    {row.status}
  </span>
);

export const AnalyticsPanel = ({
  analytics,
  open,
  onToggle,
  onSelect,
  selectedId,
}: PanelProps) => {
  const windows = analytics?.pull_tokens[0]?.windows ?? [];

  return (
    <section className={`analytics-panel ${open ? "is-open" : ""}`}>
      <button type="button" className="analytics-toggle" onClick={onToggle} aria-expanded={open}>
        {open ? <DownOutlined /> : <RightOutlined />}
        <strong>Delivery analytics</strong>
        <span>
          {open
            ? "Read activity per customer and per delivery endpoint"
            : "Show read activity per customer and per delivery endpoint"}
        </span>
      </button>

      {!open ? null : !analytics ? (
        <div className="analytics-loading">Loading activity…</div>
      ) : (
        <div className="analytics-body">
          <div className="analytics-table-wrap">
            <h3>Pull credentials</h3>
            {analytics.pull_tokens.length === 0 ? (
              <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="No pull credentials issued" />
            ) : (
              <table className="analytics-table">
                <thead>
                  <tr>
                    <th scope="col">Customer</th>
                    {windows.map((window) => (
                      // reads / rows are both shown because either alone
                      // misleads: a consumer polling with nothing to fetch is
                      // many reads and no rows, and one scripted sweep is a
                      // single read and a great many rows.
                      <th scope="col" key={window.window}>
                        {window.window}
                        <small>reads / rows</small>
                      </th>
                    ))}
                    <th scope="col">Last pull</th>
                    <th scope="col">State</th>
                  </tr>
                </thead>
                <tbody>
                  {analytics.pull_tokens.map((row) => (
                    <tr
                      key={row.node_id}
                      className={selectedId === row.node_id ? "is-selected" : ""}
                      onClick={() => onSelect(row.node_id)}
                    >
                      <th scope="row">{row.label}</th>
                      {(row.windows ?? []).map((window) => (
                        <td key={window.window} className={window.reads === 0 ? "is-quiet" : ""}>
                          {number.format(window.reads)} / {number.format(window.rows)}
                          {window.denied > 0 ? (
                            <em title="reads refused">{number.format(window.denied)} refused</em>
                          ) : null}
                        </td>
                      ))}
                      <td>{row.detail}</td>
                      <td>
                        <StatusCell row={row} />
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            )}
          </div>

          <div className="analytics-table-wrap">
            <h3>
              Delivery endpoints and spool <small>since gateway start</small>
            </h3>
            {analytics.delivery.length === 0 ? (
              <Empty
                image={Empty.PRESENTED_IMAGE_SIMPLE}
                description="No termination connectors, so nothing is delivered downstream"
              />
            ) : (
              <table className="analytics-table">
                <thead>
                  <tr>
                    <th scope="col">Destination</th>
                    <th scope="col">Delivered</th>
                    <th scope="col">Failed</th>
                    <th scope="col">Dead-lettered</th>
                    <th scope="col">Held</th>
                    <th scope="col">State</th>
                  </tr>
                </thead>
                <tbody>
                  {analytics.delivery.map((row) => (
                    <tr
                      key={row.node_id}
                      className={selectedId === row.node_id ? "is-selected" : ""}
                      onClick={() => onSelect(row.node_id)}
                    >
                      <th scope="row">{row.label}</th>
                      <td>{cell(row, "success")}</td>
                      <td>{cell(row, "failure")}</td>
                      <td className={(row.totals?.dead_lettered ?? 0) > 0 ? "is-bad" : ""}>
                        {cell(row, "dead_lettered")}
                      </td>
                      <td>{cell(row, "rows")}</td>
                      <td>
                        <StatusCell row={row} />
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            )}
          </div>

          <p className="analytics-note">{analytics.retention_note}</p>
        </div>
      )}
    </section>
  );
};

/** An absent counter is "—", never 0: the spool has no delivery counts and a
 *  delivery endpoint holds no rows, and zero would read as "none happened". */
const cell = (row: AnalyticsRow, key: string) => {
  const value = row.totals?.[key];
  return value === undefined ? "—" : number.format(value);
};
