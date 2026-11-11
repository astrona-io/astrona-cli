*** Settings ***
Documentation       What a finished run reports to Astrona, and that it arrives. Signed out, a
...                 submitted lab and a destroyed playground each send one anonymous guest
...                 run report (POST /api/cli/guest-runs); signed in, a run creates a lab
...                 session and a submit sends its result, a destroyed playground stops its
...                 clock. Each report must reach the site with the body it accepts and
...                 leave nothing queued on this machine. A site error keeps a report queued
...                 until the background sender delivers it, and --local-only sends nothing.
...
...                 The site is the fake one in SignInStack.py: it answers like the website
...                 (same paths, checks and status codes) and records every request. ASTRONA_URL
...                 points every command at it, so nothing here can reach astrona.io.
Resource            ../resources/astrona.resource
Library             ../libraries/SignInStack.py
Suite Setup         Start Reporting Suite
Suite Teardown      Finish Reporting Suite
Test Tags           full    login    reporting


*** Variables ***
${CATALOG_LAB}          ATS000/section-010/module-01/lab-01
${CATALOG_NAME}         ats-000-lab-010-01
${PLAYGROUND}           ATS000/section-010/module-01/playground
${PLAYGROUND_NAME}      ats-000-playground-010-01
${GUEST_RUNS}           /api/cli/guest-runs
${SESSIONS}             /api/cli/lab-sessions
${RESULTS}              /api/cli/lab-sessions/{id}/results
${STOP}                 /api/cli/lab-sessions/{id}/stop
# The background sender's first retry comes 15 s after a failed send.
${SEND_TIMEOUT}         90


*** Test Cases ***
Guest Lab Submit Sends A Guest Run Report
    [Documentation]    A signed-out catalog lab, submitted (graded, not passed): the site gets one
    ...    report with no sign-in, which it accepts as new (201), carrying this install's
    ...    id. No lab session is involved, and the guest queue is empty afterwards.
    [Tags]    kind    network
    [Teardown]    Destroy Lab If Test Failed    ${CATALOG_NAME}
    ${before}=    Site Request Count    ${GUEST_RUNS}
    Run Astrona    run    ${CATALOG_LAB}    --without-login    --trust    --no-browser
    ...    timeout=${SLOW_TIMEOUT}
    Kind Cluster Should Exist    ${CATALOG_NAME}
    Run Astrona    submit    ${CATALOG_LAB}    --headless    rc=2    timeout=${SLOW_TIMEOUT}
    Kind Cluster Should Not Exist    ${CATALOG_NAME}
    ${report}=    Wait Until Site Has Received    ${GUEST_RUNS}    timeout=${SEND_TIMEOUT}
    ...    kind=lab    lab=${CATALOG_LAB}    end_reason=submitted    status=${201}    valid=${True}
    ...    authorized=${False}
    ${id}=    Read Profile File    ${ENV}    install-id
    Should Be Equal    ${report}[install_id]    ${id}
    Guest Queue Should Be Empty
    ${after}=    Site Request Count    ${GUEST_RUNS}
    Should Be Equal As Integers    ${after}    ${before + 1}    one report, sent once
    Site Should Not Have Received    ${SESSIONS}

Guest Playground Report Survives A Site Error
    [Documentation]    A signed-out playground, destroyed: the site fails the first report (503),
    ...    so it stays queued on this machine; the background sender tries again and
    ...    the site accepts the same report (same idempotency key) as new.
    [Tags]    kind    network
    [Teardown]    Destroy Lab If Test Failed    ${PLAYGROUND_NAME}
    Site Fails Next Request    ${GUEST_RUNS}    503
    Run Astrona    run    ${PLAYGROUND}    --without-login    --trust    --no-browser
    ...    timeout=${SLOW_TIMEOUT}
    Kind Cluster Should Exist    ${PLAYGROUND_NAME}
    Run Astrona    destroy    ${PLAYGROUND_NAME}    --headless    timeout=${SLOW_TIMEOUT}
    Kind Cluster Should Not Exist    ${PLAYGROUND_NAME}
    ${failed}=    Wait Until Site Has Received    ${GUEST_RUNS}    timeout=${SEND_TIMEOUT}
    ...    kind=playground    lab=${PLAYGROUND}    end_reason=destroyed    status=${503}    valid=${True}
    ${queued}=    Profile Queue Length    ${ENV}    guest-runs
    Should Be Equal As Integers    ${queued}    1    the failed report must stay queued
    ${sent}=    Wait Until Site Has Received    ${GUEST_RUNS}    timeout=${SEND_TIMEOUT}
    ...    kind=playground    lab=${PLAYGROUND}    end_reason=destroyed    status=${201}    valid=${True}
    ...    authorized=${False}
    Should Be Equal    ${sent}[idempotency_key]    ${failed}[idempotency_key]
    Guest Queue Should Be Empty

Local Only Guest Run Sends Nothing
    [Documentation]    --local-only keeps a signed-out run on this machine: no guest report is
    ...    queued or sent for its submit.
    [Tags]    kind    network
    [Teardown]    Destroy Lab If Test Failed    ${CATALOG_NAME}
    ${before}=    Run Reports Received
    Run Astrona    run    ${CATALOG_LAB}    --local-only    --without-login    --trust    --no-browser
    ...    timeout=${SLOW_TIMEOUT}
    Run Astrona    submit    ${CATALOG_LAB}    --headless    rc=2    timeout=${SLOW_TIMEOUT}
    Nothing Should Have Been Reported Since    ${before}

Login Authorizes This Computer
    Sign In Through The Browser
    Set Suite Variable    ${SIGNED_IN}    ${True}

Signed In Lab Submit Sends The Result
    [Documentation]    A signed-in run creates a lab session for the account; the submit sends
    ...    its graded result (not passed) with an idempotency key, the site records it
    ...    as a new result, and the outbox is empty afterwards. No guest report.
    [Tags]    kind    network
    [Setup]    Skip Unless Signed In
    [Teardown]    Destroy Lab If Test Failed    ${CATALOG_NAME}
    ${guests}=    Site Request Count    ${GUEST_RUNS}
    Run Astrona    run    ${CATALOG_LAB}    --trust    --no-browser    timeout=${SLOW_TIMEOUT}
    Site Should Have Received    ${SESSIONS}    account=${USERNAME}    lab=${CATALOG_LAB}    status=${200}
    Run Astrona    submit    ${CATALOG_LAB}    --headless    rc=2    timeout=${SLOW_TIMEOUT}
    Kind Cluster Should Not Exist    ${CATALOG_NAME}
    Wait Until Site Has Received    ${RESULTS}    timeout=${SEND_TIMEOUT}
    ...    account=${USERNAME}    lab=${CATALOG_LAB}    passed=${False}    keyed=${True}    status=${200}
    ...    replay=${False}
    Outbox Should Be Empty
    ${after}=    Site Request Count    ${GUEST_RUNS}
    Should Be Equal As Integers    ${after}    ${guests}    a signed-in run sends no guest report

Signed In Playground Destroy Stops Its Clock
    [Documentation]    A signed-in playground creates a playground session; destroy stops its
    ...    clock as "destroyed", and the outbox is empty afterwards. No guest report.
    [Tags]    kind    network
    [Setup]    Skip Unless Signed In
    [Teardown]    Destroy Lab If Test Failed    ${PLAYGROUND_NAME}
    ${guests}=    Site Request Count    ${GUEST_RUNS}
    Run Astrona    run    ${PLAYGROUND}    --trust    --no-browser    timeout=${SLOW_TIMEOUT}
    Site Should Have Received    ${SESSIONS}    account=${USERNAME}    lab=${PLAYGROUND}    kind=playground
    ...    status=${200}
    Run Astrona    destroy    ${PLAYGROUND_NAME}    --headless    timeout=${SLOW_TIMEOUT}
    Kind Cluster Should Not Exist    ${PLAYGROUND_NAME}
    Wait Until Site Has Received    ${STOP}    timeout=${SEND_TIMEOUT}
    ...    account=${USERNAME}    lab=${PLAYGROUND}    sent_reason=destroyed    status=${200}
    Outbox Should Be Empty
    ${after}=    Site Request Count    ${GUEST_RUNS}
    Should Be Equal As Integers    ${after}    ${guests}    a signed-in run sends no guest report

Local Only Signed In Run Sends Nothing
    [Documentation]    --local-only keeps a run on this machine even when signed in: no lab
    ...    session, result, stop or guest report is sent or queued.
    [Tags]    kind    network
    [Setup]    Skip Unless Signed In
    [Teardown]    Destroy Lab If Test Failed    ${CATALOG_NAME}
    ${before}=    Run Reports Received
    Run Astrona    run    ${CATALOG_LAB}    --local-only    --trust    --no-browser    timeout=${SLOW_TIMEOUT}
    Run Astrona    submit    ${CATALOG_LAB}    --headless    rc=2    timeout=${SLOW_TIMEOUT}
    Nothing Should Have Been Reported Since    ${before}

Logout Signs This Computer Out
    [Setup]    Skip Unless Signed In
    ${result}=    Run Astrona    logout
    Should Contain    ${result.stdout}    Signed out
    Set Suite Variable    ${SIGNED_IN}    ${False}


*** Keywords ***
Start Reporting Suite
    Kind Cluster Should Not Exist    ${CATALOG_NAME}
    Kind Cluster Should Not Exist    ${PLAYGROUND_NAME}
    ${site}=    Start Sign In Stack
    # The fake site on this machine, never a real one.
    Should Match Regexp    ${site}    ^http://127\\.0\\.0\\.1:[0-9]+$
    Set Suite Variable    ${SITE}    ${site}
    ${username}=    Test Username
    Set Suite Variable    ${USERNAME}    ${username}
    Set Suite Variable    ${SIGNED_IN}    ${False}
    Prepare Astrona Environment
    # Every command talks to the fake site, whatever the caller's shell sets.
    Set To Dictionary    ${ENV}    ASTRONA_URL=${SITE}

Finish Reporting Suite
    Terminate All Processes    kill=True
    ${ready}=    Get Variable Value    ${ENV}    ${None}
    IF    $ready is not None
        Run Keyword And Ignore Error    Run Astrona    destroy    ${CATALOG_NAME}    --headless
        ...    timeout=${SLOW_TIMEOUT}
        Run Keyword And Ignore Error    Run Astrona    destroy    ${PLAYGROUND_NAME}    --headless
        ...    timeout=${SLOW_TIMEOUT}
        Run Keyword And Ignore Error    Run Astrona    logout
        Clean Up Astrona Environment
    END
    Stop Sign In Stack

Destroy Lab If Test Failed
    [Arguments]    ${name}
    IF    "${TEST STATUS}" == "FAIL"
        Run Keyword And Ignore Error    Run Astrona    destroy    ${name}    --headless
        ...    timeout=${SLOW_TIMEOUT}
    END

Skip Unless Signed In
    Skip If    not ${SIGNED_IN}    Skipped: signing in failed

Sign In Through The Browser
    ${login}=    Start Process    ${ASTRONA}    login    --headless    --site    ${SITE}    --accept-terms
    ...    cwd=${REPO_ROOT}    env=${ENV}    stdout=${CAPTURE}/login.txt    stderr=STDOUT
    ${link}    ${code}=    Wait For Sign In Link    ${CAPTURE}/login.txt
    Authorize In Browser    ${link}    ${code}
    ${result}=    Wait For Process    ${login}    timeout=${FAST_TIMEOUT}    on_timeout=terminate
    Log    ${result.stdout}
    Should Be Equal As Integers    ${result.rc}    0    astrona login exited ${result.rc}    values=False
    Should Contain    ${result.stdout}    Signed in

Guest Queue Should Be Empty
    Wait Until Keyword Succeeds    30s    1s    Profile Queue Should Be Empty    guest-runs

Outbox Should Be Empty
    Wait Until Keyword Succeeds    30s    1s    Profile Queue Should Be Empty    outbox

Profile Queue Should Be Empty
    [Arguments]    ${name}
    ${left}=    Profile Queue Length    ${ENV}    ${name}
    Should Be Equal As Integers    ${left}    0    ${left} report(s) still queued in ${name}    values=False

Run Reports Received
    [Documentation]    What run reports there are so far: how many the site has had (lab
    ...    sessions, their results and stops, guest runs) and how many wait in the
    ...    guest queue and the outbox.
    ${total}=    Set Variable    ${0}
    FOR    ${path}    IN    ${SESSIONS}    ${RESULTS}    ${STOP}    ${GUEST_RUNS}
        ${count}=    Site Request Count    ${path}
        ${total}=    Evaluate    ${total} + ${count}
    END
    ${guests}=    Profile Queue Length    ${ENV}    guest-runs
    ${outbox}=    Profile Queue Length    ${ENV}    outbox
    ${counts}=    Create Dictionary    sent=${total}    guest-runs=${guests}    outbox=${outbox}
    RETURN    ${counts}

Nothing Should Have Been Reported Since
    [Documentation]    No run report reached the site or was queued on this machine since
    ...    ${before} (Run Reports Received). A detached sender would have started at
    ...    the submit; give it time to show up.
    [Arguments]    ${before}
    Sleep    5s    a background sender would have sent by now
    ${after}=    Run Reports Received
    Dictionaries Should Be Equal    ${after}    ${before}    a --local-only run reported or queued something
