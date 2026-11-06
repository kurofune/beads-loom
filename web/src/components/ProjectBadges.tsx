import type { Project } from '@/api/types'
import { useIntegrations, type BadgePlacement } from '@/integrations'

/** The project's mounted integration badges (djinn's chip), in registry order. */
export function ProjectBadges({ project, placement }: { project: Project; placement: BadgePlacement }) {
  const { projectBadges } = useIntegrations(project)
  return (
    <>
      {projectBadges.map((badge, i) => (
        <badge.component key={i} project={project} placement={placement} />
      ))}
    </>
  )
}
