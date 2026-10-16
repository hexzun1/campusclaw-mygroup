import { useEffect, useState } from 'react'
import { createSkill, deleteSkill, errorText, listSkills, updateSkill, type Skill } from '../api/client'

export default function SkillsAdminPage() {
  const [skills, setSkills] = useState<Skill[]>([])
  const [pageError, setPageError] = useState<string | null>(null)
  const [editor, setEditor] = useState<{ id: number | null } | null>(null)
  const [source, setSource] = useState('')
  const [formError, setFormError] = useState<string | null>(null)
  const [notice, setNotice] = useState<string | null>(null)
  const [saving, setSaving] = useState(false)
  const [confirmingID, setConfirmingID] = useState<number | null>(null)

  useEffect(() => {
    let cancelled = false
    void (async () => {
      try {
        const list = await listSkills()
        if (!cancelled) setSkills(list)
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
    setNotice(null)
    setSource('')
    setEditor({ id: null })
  }

  function openEdit(s: Skill) {
    setFormError(null)
    setNotice(null)
    setSource(s.source_text)
    setEditor({ id: s.id })
  }

  function closeEditor() {
    setEditor(null)
    setSource('')
    setFormError(null)
  }

  // Choosing a file reads it into the textarea so the teacher can review and
  // correct it before anything is submitted.
  async function handleFile(e: React.ChangeEvent<HTMLInputElement>) {
    const file = e.target.files?.[0]
    if (!file) return
    setSource(await file.text())
    e.target.value = ''
  }

  async function handleSave(e: React.FormEvent<HTMLFormElement>) {
    e.preventDefault()
    if (!editor) return

    setSaving(true)
    setFormError(null)
    setNotice(null)
    try {
      if (editor.id === null) await createSkill(source)
      else await updateSkill(editor.id, source)
      setSkills(await listSkills())
      setEditor(null)
      setSource('')
      setNotice('已保存')
    } catch (err) {
      const text = errorText(err)
      if (text !== null) setFormError(text)
    } finally {
      setSaving(false)
    }
  }

  async function handleDelete(id: number) {
    setPageError(null)
    setNotice(null)
    try {
      await deleteSkill(id)
      setSkills(await listSkills())
      setNotice('已删除')
    } catch (err) {
      const text = errorText(err)
      if (text !== null) setPageError(text)
    } finally {
      setConfirmingID(null)
    }
  }

  return (
    <>
      <h1>技能管理</h1>
      {pageError && <p className="error-text">{pageError}</p>}
      {notice && <p className="success-text">{notice}</p>}

      <button type="button" onClick={openCreate}>
        新建技能
      </button>

      <ul className="admin-list">
        {skills.map((s) => (
          <li key={s.id} className="admin-item">
            <div className="admin-item-main">
              <span className="admin-item-name">{s.name}</span>
              <span className="admin-item-state">{s.enabled ? '已启用' : '已停用'}</span>
              <span className="admin-item-meta">适用条件：{s.trigger_text}</span>
            </div>
            <div className="admin-actions">
              <button type="button" onClick={() => openEdit(s)}>
                编辑
              </button>
              {confirmingID === s.id ? (
                <>
                  <button type="button" onClick={() => void handleDelete(s.id)}>
                    确认删除
                  </button>
                  <button type="button" onClick={() => setConfirmingID(null)}>
                    取消
                  </button>
                </>
              ) : (
                <button type="button" onClick={() => setConfirmingID(s.id)}>
                  删除
                </button>
              )}
            </div>
          </li>
        ))}
      </ul>

      {editor && (
        <form className="admin-form" onSubmit={handleSave}>
          <h2>{editor.id === null ? '新建技能' : '编辑技能'}</h2>

          <div className="admin-actions">
            <input type="file" onChange={handleFile} aria-label="选择技能文件" />
            <span className="admin-hint">选择文件会读入下面的文本，也可直接粘贴后提交</span>
          </div>

          <textarea
            className="skill-source"
            value={source}
            onChange={(e) => setSource(e.target.value)}
            rows={14}
            spellCheck={false}
            placeholder={'名称：\n适用条件：\n\n说明正文'}
          />

          {formError && <p className="error-text">{formError}</p>}

          <div className="admin-actions">
            <button type="submit" disabled={saving}>
              {saving ? '提交中…' : '提交'}
            </button>
            <button type="button" disabled={saving} onClick={closeEditor}>
              取消
            </button>
          </div>
        </form>
      )}
    </>
  )
}
