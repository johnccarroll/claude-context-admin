## ADDED Requirements

### Requirement: Two views of one map
The map SHALL offer a 3D view and a 2D view of the same memories, links and projects, switched from
the map's toolbar. Both views SHALL show the same memories for the current scope, kind and filter as
the memory list.

#### Scenario: Switching views
- **WHEN** the user switches from 3D to 2D or back
- **THEN** the same memories are shown, the open memory stays open and marked, and the choice is remembered on the next visit

#### Scenario: The filter applies to both views
- **WHEN** the user types in the filter box on the map
- **THEN** only matching memories are shown in either view, and memories that stay shown do not move

#### Scenario: Nothing matches
- **WHEN** the scope, kind and filter leave no memories to show but some exist
- **THEN** the map says that no memories match and offers to clear the filter

### Requirement: Default view and fallbacks
The map SHALL open in 3D when the browser can render WebGL2 without a major performance caveat and
Reduce Motion is off, and in 2D otherwise, unless the user chose a view before. When 3D cannot be
rendered, the 3D option SHALL be disabled with a tooltip that says why.

#### Scenario: No usable GPU
- **WHEN** WebGL2 is missing or only available through software rendering
- **THEN** the map opens in 2D and the 3D option is disabled

#### Scenario: Reduce Motion
- **WHEN** Reduce Motion is on and the user has not chosen a view
- **THEN** the map opens in 2D; if the user opens 3D it does not turn and shows no moving signals

### Requirement: Names on the map
Each view SHALL draw memory names on the map without hovering: the most connected and nearest first,
more as the user zooms in, and never two names overlapping each other, a project name or the map's
controls. Project names SHALL be drawn as capsules in the project's colour with the project's memory
count, so they read differently from memory names. Names SHALL be drawn as text, never as HTML.

#### Scenario: Readable at rest
- **WHEN** the map has settled at the fitted zoom
- **THEN** every project has a capsule and the most connected memories have names, with no overlaps

#### Scenario: Memory text cannot inject markup
- **WHEN** a memory's title contains HTML such as `<img src=x onerror=alert(1)>`
- **THEN** the map shows those characters as text and runs nothing

### Requirement: Focus and opening
Hovering a memory SHALL bring it and its linked memories forward and dim the rest. Clicking a memory
SHALL open it in the editor and move the view to it, clear of the editor. The open memory SHALL stay
marked until it is closed.

#### Scenario: Open from the map
- **WHEN** the user clicks a memory
- **THEN** the editor opens on it, the view moves so it is visible beside the editor, and its linked memories are named

#### Scenario: Reset view
- **WHEN** the user presses Reset view
- **THEN** the open memory closes, the view returns to its fitted starting position, and in 3D the slow turn starts again

### Requirement: Map controls
The toolbar SHALL hold the view switch, zoom out, zoom in, Reset view and a key. Each control SHALL
have a tooltip on pointer hover and on keyboard focus, and a target at least 44 px square. Zoom SHALL
move toward what the user is looking at.

#### Scenario: Tooltips
- **WHEN** the pointer rests on a toolbar control
- **THEN** its name appears after about half a second, and moving to the next control shows its name at once

### Requirement: Keyboard and screen readers
The map SHALL be usable without a pointer in both views: arrow keys move to the nearest memory in that
direction, Enter opens it, Escape clears the keyboard selection. Each move SHALL be announced with
the memory's name, project and number of links.

#### Scenario: Arrow keys
- **WHEN** the map has keyboard focus and the user presses Right
- **THEN** the nearest memory to the right is selected, named and announced

### Requirement: 3D motion
The 3D view SHALL open framed on the whole map and turn slowly. The turn SHALL pause while a memory is
under the pointer or open, resume after a few seconds without input, and stop on its own after about
30 seconds. Signals SHALL travel only along the focused memory's links. Motion SHALL be timed by
elapsed time so it runs at the same speed at any display refresh rate.

#### Scenario: Aiming at a memory
- **WHEN** the pointer moves onto a memory while the map turns
- **THEN** the turn pauses until the pointer leaves

#### Scenario: 120 Hz display
- **WHEN** the browser renders at 120 frames per second
- **THEN** the turn, the signals and every transition move at the same speed as at 60

### Requirement: Idle cost and scale
When nothing moves, the map SHALL stop drawing. It SHALL stop entirely while it is not the current
view, while the page is hidden and while the window is not visible. With 500 memories the 3D view
SHALL keep turning at 50 frames per second or more with the CPU slowed four times.

#### Scenario: Left open
- **WHEN** the map has been still for a few seconds
- **THEN** it uses under 1% of the main thread

### Requirement: Works offline and self-contained
The map SHALL load nothing from the network: its libraries and fonts are part of the cca binary, and
the 3D view is a separate file of that binary loaded only when 3D opens.

#### Scenario: No network
- **WHEN** cca runs with only loopback networking
- **THEN** both views load and work, and no request leaves 127.0.0.1
