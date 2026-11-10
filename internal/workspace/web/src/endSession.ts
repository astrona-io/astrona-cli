// What ending a session does, in words, for each kind of run. Every sentence
// states what the server and astrona actually do (labops.Submit, SendResult,
// DestroyLab); docs/reference/workspace-api.md and the browser workspace
// guide say the same. Change them together.

import type { Run } from './api'

export interface EndCopy {
  /** The top bar button. */
  button: string
  title: string
  paragraphs: string[]
  /** The confirm button names the action. */
  confirm: string
  /** Shown while it runs. */
  busy: string
}

/** What the lab's environment is made of: its cluster, or its VM(s). */
function environment(run: Run): string {
  const vms = run.targets.filter((t) => t.kind === 'vm').length
  if (vms === 0) return 'cluster'
  return vms === 1 ? 'VM' : 'VMs'
}

/** What has to run for the checks: the container engine, or the VMs. */
function engine(run: Run): string {
  return run.targets.some((t) => t.kind === 'vm') ? 'a VM stopped' : 'the container engine stopped'
}

export function endSessionCopy(run: Run, expired: boolean): EndCopy {
  if (run.playground) return playgroundCopy(run)
  const exam = run.mode === 'exam'
  const { tracking } = run.submit
  const p: string[] = []
  p.push(
    'The lab’s checks run now against your environment. The result, pass or fail, is saved on this machine with the time you spent, and you see your score and each check here.',
  )
  p.push('Submitting is final: it ends this attempt whether it passes or fails. There is no submitting again.')
  switch (tracking) {
    case 'account':
      p.push(
        'This run is linked to your Astrona account. If astrona is signed in to the same site, the result is sent to your lab page (or saved to send once Astrona can be reached).',
      )
      break
    case 'local-only':
      p.push('This run is local only: nothing is sent to your Astrona account.')
      break
    default:
      p.push('This run is not linked to an Astrona account, so the result stays on this machine.')
  }
  p.push(
    `Then the environment is removed: its ${environment(run)}, terminals and everything you changed in it are deleted. If Astrona refuses the result, or it cannot be sent or saved, the environment is left running.`,
  )
  if (exam) {
    p.push('Your exam time is recorded at the moment you submit, and the timer stops.')
  }
  if (run.rules.strict && expired) {
    p.push('The time limit has passed. In this strict exam a submission after the deadline cannot pass.')
  }
  p.push(`If the checks cannot run, for example because ${engine(run)}, nothing is recorded or sent and the lab keeps running.`)
  return {
    button: exam ? 'Submit exam' : 'Submit lab',
    title: exam ? 'Submit your exam?' : 'Submit this lab?',
    paragraphs: p,
    confirm: 'Submit and grade',
    busy: 'Grading… This can take a few minutes. Keep this page open.',
  }
}

function playgroundCopy(run: Run): EndCopy {
  const p = [
    'A playground has no checks: nothing is graded or submitted.',
    `Ending it deletes the environment now: its ${environment(run)}, terminals and everything in it. This cannot be undone.`,
  ]
  if (run.submit.tracking === 'account') {
    p.push('Your Astrona account is told the playground ended, with the time and resources it used.')
  } else if (run.submit.tracking === 'local-only') {
    p.push('This run is local only: nothing is sent to your Astrona account.')
  }
  return {
    button: 'End playground',
    title: 'End this playground?',
    paragraphs: p,
    confirm: 'End and delete playground',
    busy: 'Deleting the playground…',
  }
}

/** Removing a lab's environment without grading (POST /destroy). */
export function removeCopy(run: Run): EndCopy {
  const p = [
    `This deletes the lab’s ${environment(run)}, its terminals and everything you changed in it. This cannot be undone.`,
    'Nothing is graded or submitted. Results you already submitted stay as they are.',
  ]
  if (run.submit.tracking === 'account') {
    p.push(
      'Your Astrona lab page is told the lab was removed: the attempt stops showing as running, and it does not count unless it passed before.',
    )
  }
  if (run.mode === 'exam') p.push('The exam timer is cleared.')
  p.push(`To practise again afterwards, start a new run with astrona run in your terminal.`)
  return {
    button: 'Remove environment',
    title: 'Remove the environment?',
    paragraphs: p,
    confirm: 'Remove environment',
    busy: 'Removing the environment…',
  }
}

/**
 * How to start this lab again from a terminal: the run command it was
 * started with (a path, Git source or catalog ID — never the cluster name).
 */
export function runAgainCommand(run: Run | null): string {
  return run?.runAgain || 'astrona run <lab path or ID>'
}
