import { useEffect, useState } from 'react'
import {
  createAgent,
  errorText,
  listAgents,
  listMaterials,
  listSkills,
  updateAgent,
  type Agent,
  type AgentInput,
  type MaterialSummary,
  type Skill,
} from '../api/client'

interface AgentForm {
  // null while creating; the id of the agent being edited otherwise.
  id: number | null
  name: string
  prompt: string
  enabled: boolean
  skillIDs: number[]
  materialIDs: number[]
}

export default function AgentsAdminPage() {
  const [agents, setAgents] = useState<Agent[]>([])
  const [skills, setSkills] = useState<Skill[]>([])
  const [materials, setMaterials] = useState<MaterialSummary[]>([])
  const [pageError, setPageError] = useState<string | null>(null)
  const [form, setForm] = useState<AgentForm | null>(null)
  const [formError, setFormError] = useState<string | null>(null)
  const [saving, setSaving] = useState(false)

  useEffect(() => {
    let cancelled = false
    void (async () => {
      try {
        const [ags, sks, mats] = await Promise.all([listAgents(), listSkills(), listMaterials('')])
        if (cancelled) return
        setAgents(ags)
        setSkills(sks)
        setMaterials(mats)
      } catch (err) {
        if (cancelled) return
        const text = errorText(err)
        if (text !== null) setPageError(text)
      }
    })()
    return () => {
      cancelled = true
    }
  }, [])

  function openCreate() {
    setFormError(null)
    setForm({ id: null, name: '', prompt: '', enabled: true, skillIDs: [], materialIDs: [] })
  }

  function openEdit(a: Agent) {
    setFormError(null)
    setForm({ id: a.id, name: a.name, prompt: a.prompt, enabled: a.enabled, skillIDs: a.skill_ids, materialIDs: a.material_ids })
  }

  function toggle(ids: number[], id: number): number[] {
    return ids.includes(id) ? ids.filter((x) => x !== id) : [...ids, id]
  }

  async function handleSave(e: React.FormEvent<HTMLFormElement>) {
    e.preventDefault()
    if (!form) return
    const input: AgentInput = {
      name: form.name,
      prompt: form.prompt,
      enabled: form.enabled,
      skill_ids: form.skillIDs,
      material_ids: form.materialIDs,
    }

    setSaving(true)
    setFormError(null)
    try {
      if (form.id === null) await createAgent(input)
      else await updateAgent(form.id, input)
      setAgents(await listAgents())
      setForm(null)
    } catch (err) {
      const text = errorText(err)
      if (text !== null) setFormError(text)
    } finally {
      setSaving(false)
    }
  }

  return (
    <>
      <h1>智能体管理</h1>
      {pageError && <p className="error-text">{pageError}</p>}

      <button type="button" onClick={openCreate}>
        新建智能体
      </button>

      <ul className="admin-list">
        {agents.map((a) => (
          <li key={a.id} className="admin-item">
            <div className="admin-item-main">
              <span className="admin-item-name">{a.name}</span>
              <span className="admin-item-state">{a.enabled ? '已启用' : '已停用'}</span>
              <span className="admin-item-meta">技能 {a.skill_ids.length} 项，材料 {a.material_ids.length} 项</span>
            </div>
            <button type="button" onClick={() => openEdit(a)}>
              编辑
            </button>
          </li>
        ))}
      </ul>

      {form && (
        <form className="admin-form" onSubmit={handleSave}>
          <h2>{form.id === null ? '新建智能体' : '编辑智能体'}</h2>

          <label>
            名称
            <input value={form.name} onChange={(e) => setForm({ ...form, name: e.target.value })} maxLength={64} />
          </label>

          <label>
            提示词
            <textarea
              value={form.prompt}
              onChange={(e) => setForm({ ...form, prompt: e.target.value })}
              maxLength={2000}
              rows={6}
            />
          </label>

          <label className="admin-check">
            <input
              type="checkbox"
              checked={form.enabled}
              onChange={(e) => setForm({ ...form, enabled: e.target.checked })}
            />
            启用
          </label>

          <fieldset className="admin-fieldset">
            <legend>绑定技能</legend>
            {skills.map((s) => (
              <label key={s.id} className="admin-check">
                <input
                  type="checkbox"
                  checked={form.skillIDs.includes(s.id)}
                  onChange={() => setForm({ ...form, skillIDs: toggle(form.skillIDs, s.id) })}
                />
                {s.name}
                {!s.enabled && '（已停用）'}
              </label>
            ))}
            {skills.length === 0 && <p className="chat-empty">本班还没有技能</p>}
          </fieldset>

          <fieldset className="admin-fieldset">
            <legend>绑定材料</legend>
            {materials.map((m) => (
              <label key={m.id} className="admin-check">
                <input
                  type="checkbox"
                  checked={form.materialIDs.includes(m.id)}
                  onChange={() => setForm({ ...form, materialIDs: toggle(form.materialIDs, m.id) })}
                />
                {m.title}
              </label>
            ))}
            {materials.length === 0 && <p className="chat-empty">本班还没有材料</p>}
          </fieldset>

          {formError && <p className="error-text">{formError}</p>}

          <div className="admin-actions">
            <button type="submit" disabled={saving}>
              {saving ? '保存中…' : '保存'}
            </button>
            <button type="button" disabled={saving} onClick={() => setForm(null)}>
              取消
            </button>
          </div>
        </form>
      )}
    </>
  )
}
