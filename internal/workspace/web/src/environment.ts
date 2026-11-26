// What a lab is made of (Run.environment, Launch.environment), as label and
// value rows for the launch page and the playground panel. Only what the
// server sent: an absent or empty field gives no row.

import type { Environment } from './api'

export interface Spec {
  /** "" for a continuation row (a second image). */
  label: string
  value: string
}

function plural(n: number, one: string, many: string): string {
  return `${n} ${n === 1 ? one : many}`
}

export function envSpecs(env: Environment | undefined | null): Spec[] {
  if (!env) return []
  const out: Spec[] = []
  if (env.kubernetesVersion) {
    out.push({ label: 'Kubernetes', value: env.runtime ? `${env.kubernetesVersion} (${env.runtime})` : env.kubernetesVersion })
  }
  if (env.engine) out.push({ label: 'Container engine', value: env.engine })
  const nodes = env.nodes ?? []
  if (nodes.length) {
    const control = nodes.filter((n) => n.role === 'control-plane').length
    const workers = nodes.length - control
    const parts: string[] = []
    if (control) parts.push(plural(control, 'control plane', 'control planes'))
    if (workers) parts.push(plural(workers, 'worker', 'workers'))
    out.push({ label: 'Nodes', value: parts.join(', ') })
  }
  if (env.network) out.push({ label: 'Network', value: env.network })
  for (const vm of env.vms ?? []) {
    const bits = [vm.os, vm.cpus ? plural(vm.cpus, 'CPU', 'CPUs') : '', vm.memoryMiB ? memory(vm.memoryMiB) : '']
    const value = bits.filter(Boolean).join(' · ')
    if (value) out.push({ label: vm.name ? `VM ${vm.name}` : 'VM', value })
  }
  ;(env.images ?? []).filter(Boolean).forEach((image, i) => out.push({ label: i === 0 ? 'Images' : '', value: image }))
  return out
}

function memory(mib: number): string {
  return mib >= 1024 && mib % 1024 === 0 ? `${mib / 1024} GiB` : `${mib} MiB`
}
