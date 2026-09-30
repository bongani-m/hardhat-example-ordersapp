import { FormEvent, useEffect, useState } from "react";

type Order = {
  id: number;
  item: string;
  status: string;
  created_at: string;
};

type Health = Record<string, string>;

const services = ["hardhatdb", "hardhatkv", "hardhatq"] as const;

export function App() {
  const [item, setItem] = useState("");
  const [orders, setOrders] = useState<Order[]>([]);
  const [listError, setListError] = useState("");
  const [formError, setFormError] = useState("");
  const [saving, setSaving] = useState(false);
  const [tick, setTick] = useState(0);
  const [health, setHealth] = useState<Health | null>(null);
  const [healthOk, setHealthOk] = useState(false);

  const pending = orders.some((order) => order.status === "new");

  useEffect(() => {
    const ctrl = new AbortController();
    let timer = 0;

    async function load() {
      try {
        const res = await fetch("/orders", { signal: ctrl.signal });
        if (!res.ok) {
          throw new Error("list failed");
        }
        const data = (await res.json()) as Order[];
        setOrders(data);
        setListError("");
      } catch (err) {
        if (ctrl.signal.aborted) {
          return;
        }
        setListError(err instanceof Error ? err.message : "list failed");
      }
    }

    void load();
    if (pending) {
      timer = window.setInterval(() => void load(), 1000);
    }
    return () => {
      ctrl.abort();
      window.clearInterval(timer);
    };
  }, [pending, tick]);

  useEffect(() => {
    const ctrl = new AbortController();

    async function load() {
      try {
        const res = await fetch("/up", { signal: ctrl.signal });
        const data = (await res.json()) as Health;
        setHealth(data);
        setHealthOk(res.ok);
      } catch {
        if (ctrl.signal.aborted) {
          return;
        }
        setHealth(null);
        setHealthOk(false);
      }
    }

    void load();
    const timer = window.setInterval(() => void load(), 5000);
    return () => {
      ctrl.abort();
      window.clearInterval(timer);
    };
  }, []);

  async function onSubmit(event: FormEvent) {
    event.preventDefault();
    const name = item.trim();
    if (!name) {
      setFormError("item is required");
      return;
    }
    setSaving(true);
    setFormError("");
    try {
      const res = await fetch("/orders", {
        method: "POST",
        headers: { "content-type": "application/json" },
        body: JSON.stringify({ item: name }),
      });
      const data = (await res.json()) as { error?: string };
      if (!res.ok) {
        throw new Error(data.error || "create failed");
      }
      setItem("");
      setTick((value) => value + 1);
    } catch (err) {
      setFormError(err instanceof Error ? err.message : "create failed");
    } finally {
      setSaving(false);
    }
  }

  return (
    <main>
      <header>
        <h1>Orders</h1>
        <p className={healthOk ? "health ok" : "health"}>
          {health
            ? services
                .map((name) => `${name} ${health[name] ?? "unknown"}`)
                .join(" · ")
            : "API unreachable"}
        </p>
      </header>

      <form onSubmit={onSubmit}>
        <label htmlFor="item">Item</label>
        <div className="row">
          <input
            id="item"
            name="item"
            value={item}
            maxLength={255}
            placeholder="notebook"
            onChange={(event) => setItem(event.target.value)}
          />
          <button type="submit" disabled={saving}>
            {saving ? "Saving…" : "Create"}
          </button>
        </div>
        {formError ? <p className="error">{formError}</p> : null}
      </form>

      <section>
        <h2>Recent</h2>
        {listError ? <p className="error">{listError}</p> : null}
        {orders.length === 0 && !listError ? <p className="empty">No orders yet.</p> : null}
        <ul>
          {orders.map((order) => (
            <li key={order.id}>
              <div>
                <span className="item">{order.item}</span>
                <span className="meta">
                  #{order.id} · {formatTime(order.created_at)}
                </span>
              </div>
              <span className={order.status === "done" ? "status done" : "status new"}>
                {order.status}
              </span>
            </li>
          ))}
        </ul>
      </section>
    </main>
  );
}

function formatTime(value: string) {
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) {
    return value;
  }
  return date.toLocaleString();
}
