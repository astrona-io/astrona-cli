*** Settings ***
Documentation       The binary starts, explains itself and judges this machine and a lab config
...                 without creating anything.
Resource            ../resources/astrona.resource
Suite Setup         Prepare Astrona Environment
Suite Teardown      Clean Up Astrona Environment
Test Tags           smoke


*** Test Cases ***
Operational Commands Refuse To Run Headless Without Accepted Terms
    [Documentation]    CI must accept the data-loss notice explicitly, never by default.
    ...    Runs first: acceptance is remembered in astrona's state for later commands.
    ${result}=    Run Astrona    check    --headless    rc=1    accept_terms=${False}
    Should Contain    ${result.stderr}    --accept-terms

Version Is Reported
    ${result}=    Run Astrona    --version
    Should Match Regexp    ${result.stdout}    ^astrona version \\S+

Help Lists Every Command Group
    ${result}=    Run Astrona    --help
    FOR    ${group}    IN    Get started:    Take a lab:    Manage running labs:
    ...    Write labs (authors and teachers):    Troubleshoot:
        Should Contain    ${result.stdout}    ${group}
    END

Terms Are Readable Without Accepting Them
    ${result}=    Run Astrona    terms
    Should Not Be Empty    ${result.stdout}

Example Lab Config Validates
    ${report}=    Run Astrona Json    validate    -c    ${EXAMPLE_LAB}
    Log    ${report}

Machine Can Run Kind Labs
    [Tags]    smoke    kind
    Run Astrona    check    --headless    timeout=${SLOW_TIMEOUT}
