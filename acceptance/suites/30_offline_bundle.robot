*** Settings ***
Documentation       Offline bundles: pack a lab with what it downloads, then verify the bundle.
Resource            ../resources/astrona.resource
Suite Setup         Prepare Astrona Environment
Suite Teardown      Clean Up Astrona Environment
Test Tags           smoke    kind


*** Variables ***
${BUNDLE}           ${OAT_OUTPUT}/bundle-${LAB_NAME}.tar.gz


*** Test Cases ***
Bundle Create Packs The Lab
    Run Astrona    bundle    create    -c    ${EXAMPLE_LAB}    -o    ${BUNDLE}    --node-image    ${NODE_IMAGE}
    ...    --headless    --accept-terms    timeout=${SLOW_TIMEOUT}
    File Should Exist    ${BUNDLE}

Bundle Inspect Verifies The Bundle
    ${result}=    Run Astrona    bundle    inspect    ${BUNDLE}    timeout=${SLOW_TIMEOUT}
    Should Contain    ${result.stdout}    ${LAB_NAME}
