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

export function endSessionCopy(run: Run, expired: boolean): EndCopy {
  if (run.playground) return playgroundCopy(run)
  const exam = run.mode === 'exam'
  const { tracking, keepEnvironment } = run.submit
  const p: string[] = []
  p.push(
    'The lab’s checks run now against your environment. The result is saved on this machine as a new attempt, and you see your score and each check here.',
  )
  switch (tracking) {
    case 'account':
      p.push(
        'This run is linked to your Astrona account. If astrona is signed in to the same site, the result is sent to your lab page (or saved to send once Astrona can be reached). A pass finishes this attempt. A fail keeps it open: fix the lab and submit again.',
      )
      break
    case 'local-only':
      p.push(
        'This run is local only: nothing is sent to your Astrona account. A pass saved here finishes this attempt; a fail keeps it open, so you can fix the lab and submit again.',
      )
      break
    default:
      p.push(
        'This run is not linked to an Astrona account, so the result stays on this machine. The attempt stays open: you can keep working and submit again.',
      )
  }
  if (keepEnvironment) {
    p.push(
      `This lab keeps its environment after a submission (teardown.keepCluster). Remove it yourself later with astrona destroy ${run.lab}.`,
    )
  } else if (tracking === 'none') {
    p.push(
      `The environment keeps running after you submit. Remove it from the results page, or with astrona destroy ${run.lab}.`,
    )
  } else {
    p.push(
      'When a pass finishes the attempt, the environment is removed: its cluster, terminals and everything you changed in it are deleted. After a fail, or a result that is not sent, the environment keeps running.',
    )
  }
  if (exam) {
    let line = 'Your exam time is recorded at the moment you submit.'
    if (!keepEnvironment && tracking !== 'none') line += ' A pass stops the timer when the environment is removed; after a fail it keeps running.'
    else line += ' The timer keeps running until the environment is removed.'
    p.push(line)
  }
  if (run.rules.strict && expired) {
    p.push('The time limit has passed. In this strict exam a submission after the deadline cannot pass.')
  }
  p.push('If the checks cannot run, for example because the container engine stopped, nothing is recorded or sent and the lab keeps running.')
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
    'Ending it deletes the environment now: its cluster, terminals and everything in it. This cannot be undone.',
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
    'This deletes the lab’s cluster, its terminals and everything you changed in it. This cannot be undone.',
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
