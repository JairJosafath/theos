// What the story's diagram on screen shows, and what its boxes do.

import type { View } from '../lib/hooks'

export function ViewCaption({
  view,
  systemTitle,
  opensPages,
}: {
  view: View
  systemTitle?: string
  opensPages: boolean
}) {
  if (view === 'context') {
    return (
      <div className="view-head">
        <h2 className="view-title">System context: the big picture</h2>
        <p className="view-caption">
          Who uses the software, and which software systems are involved. Each arrow says who uses what, and what for.{' '}
          {opensPages ? 'Open a system for its page' : 'Select a box for details'}, or <em>Look inside</em> a system to
          see what it is made of.
        </p>
      </div>
    )
  }
  return (
    <div className="view-head">
      <h2 className="view-title">Containers of {systemTitle ?? 'a system'}</h2>
      <p className="view-caption">
        The applications (containers) and data stores inside the dashed box make up this system. The factory gives each
        container its own Git repository, started from templates. People and grey systems around it are outside. Hover
        over a container to see how the factory is getting on{opensPages ? ', or open it for its page' : ''}.
      </p>
    </div>
  )
}
