import { Plus, Trash2 } from "lucide-react";
import { useEffect, useState } from "react";
import { Badge, Field, Modal } from "../components/ui";
import { api } from "../lib/api";
import type { User } from "../types";
export default function Accounts({
  admin,
  onError,
  onDone,
}: {
  admin: boolean;
  onError: (e: unknown) => void;
  onDone: (s: string) => void;
}) {
  const [list, setList] = useState<User[]>([]);
  const [edit, setEdit] = useState<{
    id: number;
    name: string;
    role: string;
    password: string;
  }>();
  const [busy, setBusy] = useState(false);
  async function load() {
    try {
      setList(await api("users"));
    } catch (e) {
      onError(e);
    }
  }
  useEffect(() => {
    void load();
  }, []);
  return (
    <>
      <div className="page-heading">
        <div>
          <span className="eyebrow">ACCESS</span>
          <h1>Users</h1>
          <p>Local accounts, clear permissions, revocable sessions.</p>
        </div>
        {admin && (
          <button
            className="primary"
            onClick={() =>
              setEdit({ id: 0, name: "", role: "view-only", password: "" })
            }
          >
            <Plus size={16} />
            Add user
          </button>
        )}
      </div>
      <div className="table-wrap">
        <table className="data-table">
          <thead>
            <tr>
              <th>User</th>
              <th>Role</th>
              <th>Access</th>
              <th />
            </tr>
          </thead>
          <tbody>
            {list.map((u) => (
              <tr key={u.id}>
                <td>
                  <div className="user-cell">
                    <span className="avatar light">
                      {u.name.slice(0, 2).toUpperCase()}
                    </span>
                    <strong>{u.name}</strong>
                  </div>
                </td>
                <td>
                  <Badge
                    tone={u.role === "administrator" ? "green" : "neutral"}
                  >
                    {u.role === "administrator"
                      ? "Administrator"
                      : u.role === "network-admin"
                        ? "Network admin"
                        : "View-only"}
                  </Badge>
                </td>
                <td>
                  {u.role === "administrator"
                    ? "Full access, including user accounts"
                    : u.role === "network-admin"
                      ? "Network configuration; no account management"
                      : "Read-only access to the application"}
                </td>
                <td>
                  {admin && (
                    <div className="row-actions">
                      <button
                        className="link"
                        onClick={() => setEdit({ ...u, password: "" })}
                      >
                        Edit
                      </button>
                      <button
                        className="icon danger"
                        aria-label={`Delete user ${u.name}`}
                        onClick={async () => {
                          if (
                            !confirm(
                              `Delete ${u.name} and revoke their sessions?`,
                            )
                          )
                            return;
                          try {
                            await api("users?id=" + u.id, "DELETE");
                            onDone("User deleted.");
                            await load();
                          } catch (e) {
                            onError(e);
                          }
                        }}
                      >
                        <Trash2 size={16} />
                      </button>
                    </div>
                  )}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      {edit && (
        <Modal
          title={edit.id ? "Edit user" : "Add user"}
          onClose={() => setEdit(undefined)}
        >
          <form
            onSubmit={async (e) => {
              e.preventDefault();
              setBusy(true);
              try {
                await api("users", edit.id ? "PUT" : "POST", edit);
                setEdit(undefined);
                onDone(
                  "User saved. Existing sessions are revoked when an account is changed.",
                );
                await load();
              } catch (e) {
                onError(e);
              } finally {
                setBusy(false);
              }
            }}
          >
            <Field label="Username">
              <input
                autoFocus
                required
                minLength={2}
                maxLength={64}
                value={edit.name}
                onChange={(e) => setEdit({ ...edit, name: e.target.value })}
              />
            </Field>
            <Field label="Role">
              <select
                value={edit.role}
                onChange={(e) => setEdit({ ...edit, role: e.target.value })}
              >
                <option value="view-only">View-only</option>
                <option value="network-admin">Network admin</option>
                <option value="administrator">Administrator</option>
              </select>
            </Field>
            <Field
              label={edit.id ? "New password (optional)" : "Password"}
              help="At least 12 characters. Account updates revoke existing sessions."
            >
              <input
                type="password"
                autoComplete="new-password"
                required={!edit.id}
                minLength={12}
                maxLength={256}
                value={edit.password}
                onChange={(e) => setEdit({ ...edit, password: e.target.value })}
              />
            </Field>
            <div className="modal-actions">
              <button
                type="button"
                className="secondary"
                onClick={() => setEdit(undefined)}
              >
                Cancel
              </button>
              <button className="primary" disabled={busy}>
                Save user
              </button>
            </div>
          </form>
        </Modal>
      )}
    </>
  );
}
