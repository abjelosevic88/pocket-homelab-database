import { Modal } from './ui'

const GROUPS: { title: string; keys: [string[], string][] }[] = [
  { title: 'Go to', keys: [[['h'], 'Overview'], [['c'], 'Connections'], [['d'], 'On this server'], [['s'], 'Saved queries'], [['y'], 'History'], [['a'], 'Changes'], [[','], 'Settings'], [['/'], 'Search & commands'], [['⌘', 'K'], 'Search & commands']] },
  { title: 'New', keys: [[['n'], 'Add a connection'], [['q'], 'New SQL query']] },
  { title: 'SQL editor', keys: [[['⌘', '↵'], 'Run (the selection, or everything)'], [['⇧', '↵'], 'Run the statement under the cursor'], [['⌘', 'S'], 'Save the query'], [['tab'], 'Indent']] },
  { title: 'Tables', keys: [[['dbl-click'], 'Edit a cell (when changes are on)'], [['↵'], 'Keep the edit'], [['esc'], 'Drop the edit']] },
  { title: 'Window', keys: [[['['], 'Show or hide the sidebar'], [['esc'], 'Close a dialog or menu']] },
  { title: 'Help', keys: [[['?'], 'This sheet']] },
]

export function ShortcutsSheet({ onClose }: { onClose: () => void }) {
  return (
    <Modal title="Keyboard shortcuts" onClose={onClose} size="lg">
      <div className="shortcuts">
        {GROUPS.map(g => (
          <section key={g.title}>
            <h4 className="sc-h">{g.title}</h4>
            {g.keys.map(([keys, label]) => (
              <div key={label + keys.join('')} className="sc-row"><span>{label}</span><span className="sc-keys">{keys.map(k => <kbd key={k}>{k}</kbd>)}</span></div>
            ))}
          </section>
        ))}
      </div>
    </Modal>
  )
}
