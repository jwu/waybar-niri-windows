# niri-windows module for Waybar

This is a module for [Waybar](https://github.com/Alexays/Waybar) that displays a window minimap for the current [niri](https://github.com/YaLTeR/niri) workspace.

![Image of the module](screenshot.png)
![Image of the module in text mode](screenshot-text.png)

> [!IMPORTANT]
> niri ≥ v25.08 is required (for the window locations in IPC messages to be available).

## Installation

If you're using an amd64 system, you can get a pre-built binary from the
[releases page](https://github.com/calico32/waybar-niri-windows/releases).
Download and place `waybar-niri-windows.so` anywhere permanent;
`~/.config/waybar` is a good place.

### From source

If you'd like to build from source (or if you're on a different platform):

1. Install GTK 3 + development headers (`apt install libgtk-3-dev`, `pacman -S gtk3`, etc.).
2. Clone this repository.
3. Run `make` to produce `waybar-niri-windows.so`. This might take a while (thanks to cgo).

Move the library anywhere permanent, e.g. `~/.config/waybar`.

## Configuration

Add a CFFI module to your Waybar config:

```jsonc
{
  "modules-left": ["cffi/niri-windows"],
  "cffi/niri-windows": {
    // path where you placed the .so file
    "module_path": "/home/calico32/.config/waybar/waybar-niri-windows.so",
    // configure the module's behavior
    "options": {
      // set the module mode
      // "graphical" (default): draw a minimap of windows in the current workspace
      // "text": draws symbols and a focus indicator for each column (mirrors v1 behavior)
      "mode": "graphical",

      // ======= graphical mode options =======
      //  when to show floating windows
      //   - "always": always show floating window view, even if there are no floating windows
      //   - "auto" (default): show floating window view if there are floating windows on the current workspace
      //   - "never": never show floating windows
      "show-floating": "auto",
      // pick where the floating windows be shown relative to tiled windows
      //   - "left": show floating windows on the left
      //   - "right" (default): show floating windows on the right
      "floating-position": "right",
      // set minimum size of windows, in pixels (default: 1, minimum: 1)
      // if this value is too large to fit all windows (e.g. in a column with many windows),
      // it will be reduced
      "minimum-size": 1,
      // set spacing between windows/columns, in pixels (default: 1, minimum: 0)
      // if this value is too large, it will be reduced
      "spacing": 1,
      // set minimum size of windows, in pixels, to draw icons for (default: 0, minimum: 0)
      // if unset or 0, icons will only be drawn for tiled windows that are the only one in their column
      // if 1+, icons will be drawn for all windows where w >= icon-minimum-size and h >= icon-minimum-size
      // icons must be set in the "rules" section below for this to have any effect
      "icon-minimum-size": 0,
      // account for borders when calculating window sizes; see note below (default: 0, minimum: 0)
      "column-borders": 0, // border on .column
      "floating-borders": 0, // border on .floating
      // trigger actions on tile click (see https://yalter.github.io/niri/niri_ipc/enum.Action.html for available actions)
      // only actions that take a single window ID are supported
      // set to an empty string to disable
      "on-tile-click": "FocusWindow", // (default: FocusWindow)
      "on-tile-middle-click": "CloseWindow", // (default: CloseWindow)
      "on-tile-right-click": "", // (default: none)
      // add CSS classes/icons to windows based on their App ID/Title (see `niri msg windows`)
      // Go regular expression syntax is supported for app-id and title (see https://pkg.go.dev/regexp/syntax)
      // rules are checked in the order they are defined - first match wins and checking stops
      // set "continue" to true to also check and apply subsequent rules even if this rule matches
      // if multiple rules with icons are applied, the first one will be used
      // *icons are not drawn for floating windows by default*; set "icon-minimum-size" to enable (see above)
      "rules": [
        // .alacritty will be added to all windows with the App ID "Alacritty"
        //  will be drawn in windows that match
        { "app-id": "Alacritty", "class": "alacritty", "icon": "" },
        // .firefox will be added to all windows with the App ID "firefox"
        // subsequent rules are also checked and applied for firefox windows
        { "app-id": "firefox", "class": "firefox", "continue": true },
        // .youtube-music will be added to all windows that have "YouTube Music" at the end of their title
        //  will be drawn in windows that match
        { "title": "YouTube Music$", "class": "youtube-music", "icon": "" }
      ],

      // ======= text mode options =======
      // customize the symbols used to draw the columns/windows
      "symbols": {
        "unfocused": "⋅",
        "focused": "⊙",
        "unfocused-floating": "∗",
        "focused-floating": "⊛",
        // text to display when there are no windows on the current workspace
        // if this is an empty string (default), the module will be hidden when there are no windows
        "empty": ""
      }
    },
    "actions": {
      // use niri IPC action names to trigger them (see https://yalter.github.io/niri/niri_ipc/enum.Action.html for available actions)
      // any action that has no fields is supported
      "on-scroll-up": "FocusColumnLeft",
      "on-scroll-down": "FocusColumnRight"
      // in graphical mode, don't configure click actions here—they're handled by the module above
    }
  }
}
```

### Styling

Use these selectors in your CSS to style the module.

#### Graphical mode

**Windows:**

- `.cffi-niri-windows .tile`: any window, tiled or floating
- `.cffi-niri-windows .column .tile`: tiled window
- `.cffi-niri-windows .floating .tile`: floating window
- `.cffi-niri-windows .<custom-class>`: any window with a custom class (see `rules` in the config)
- Add `:hover` (mouse hover) or `:active` (focused) to any of the above selectors to style those states.
- Use `:first-child`, `:last-child`, and `:nth-child(n)` to style the first, last, or nth window in a column.
- Use `:only-child` to style the window when it is the only window in a column.
- Add `.urgent` to style windows marked as urgent.
- Add `.light`, `.medium`, or `.heavy` to style the tiles of windows that are working (see [Window activity](#window-activity)).

**Containers:**

- `.cffi-niri-windows .column`: column of tiled windows
- `.cffi-niri-windows .floating`: floating window view
- Add `:active` to any of the above selectors to style that container when they contain the focused window.
- Use `:first-child`, `:last-child`, and `:nth-child(n)` to style the first, last, or nth container.
- Use `:only-child` to style the container when it is the only container.

For example:

```css
.cffi-niri-windows .tile {
  background-color: rgba(255, 255, 255, 0.5);
}
.cffi-niri-windows .tile:hover {
  background-color: rgba(255, 255, 255, 0.7);
}
.cffi-niri-windows .tile:active {
  background-color: rgb(255, 255, 255);
}
```

> [!NOTE]
>
> Adding borders to containers may cause them to overflow the bar height. If
> this happens, set these config options to account for the borders:
>
> - Set `column-borders` to the total height (in pixels) of the top and bottom border on `.column`
> - Set `floating-borders` to the total height (in pixels) of the top and bottom border on `.floating`
>
> For example, for the following CSS:
>
> ```css
> .cffi-niri-windows .column {
>   border: 1px solid rgba(255, 255, 255, 0.85);
> }
>
> .cffi-niri-windows .floating {
>   border: 2px solid rgba(255, 255, 255, 0.85);
> }
> ```
>
> Set `column-borders` to `2` and `floating-borders` to `4`.

**Text mode** (be sure to specify a font that supports the symbols you're using):

- `.cffi-niri-windows label`

```css
.cffi-niri-windows label {
  font-family: Uiua386;
  font-size: 18px;
  margin-top: -2px;
}
```

### Window activity

Once a second, the module samples the process trees behind the windows on the
bar's monitor and adds a class to the tile of a window that is working, so that
work in progress is visible without demanding attention:

- `.cffi-niri-windows .tile.light`: `0.08` to `0.2` of a core
- `.cffi-niri-windows .tile.medium`: `0.2` to `1.5` cores
- `.cffi-niri-windows .tile.heavy`: `1.5` cores and up, taken on the first sample
  that reaches it
- idle: no class at all

The level is the median of the last five samples, so a burst of a second cannot
light a tile, and work that keeps going shows up within a couple of seconds. The
classes are not styled by default; see [`procs/procs.go`](procs/procs.go) for the
boundaries themselves and where they come from.

#### What a window is measured by

By default, a window is measured by the process niri reports for it — the
application. For an application that keeps every window in one process (Ghostty
in single-instance mode, a browser), that pid is the same for all of them, and a
build in one window shows on every window of that application: a pid is all niri
knows about a window's process.

A shell can do better for its own window. It knows which window it runs in (the
one whose title its terminal shows) and the module reads titles from niri, so the
shell can say which pid to measure by writing it into the title, in characters
that nothing renders. The shell side is a small zsh file, `zsh-announce.zsh`, with
the shell configuration rather than in this repository (this fork's copy is
installed as `~/.config/waybar/zsh-announce.zsh` from
[jwu/configs](https://github.com/jwu/configs/blob/main/linux/.config/waybar/zsh-announce.zsh)),
and a shell picks it up by sourcing it at the very end of `~/.zshrc`:

```sh
# ~/.zshrc, at the very end
[ -r ~/.config/waybar/zsh-announce.zsh ] && source ~/.config/waybar/zsh-announce.zsh
```

The file writes the same title the prompt itself writes — oh-my-zsh and most
themes publish that text in `ZSH_THEME_TERM_TITLE_IDLE`, and without it the file
falls back to Ghostty's own format, the truncated working directory — with the
announcement appended, so a window's visible title does not change. Ghostty's
shell integration can go on writing its own title: this file's write comes after
it. Command titles are left alone, so while a command runs there is no
announcement in the title and the module uses the pid the shell announced at the
last prompt. The announcement itself is described in
[`module/marker.go`](module/marker.go).

Anything else that owns a window's title can announce itself the same way, by
appending the marker of whichever pid the tile should follow (the bytes are in
[`module/marker.go`](module/marker.go)). The announcement is part of the title
string even though nothing draws it, so a script or a window switcher that
matches titles sees the tag characters; niri's overview and the bar's tooltip do
not.

A window that never announces anything keeps the default behaviour: it is
measured by the whole application's process tree — unless another window of that
same application did announce, in which case that tree is known to include the
other window's work, and this one is left without a level instead of showing work
that is not its own. Announcing is a nicety, and the module works without it.

#### Remembered announcements

The announcement is only in the title for as long as the shell is the program
that owns it: a terminal shows the title of whatever runs in it, so an editor or
an agent that takes the title over hides the announcement until it exits. The
module therefore remembers the pid each window announced last, in
`~/.cache/waybar-niri-windows/announced.json`, so that a window keeps its own
measurement across a restart of the bar — a fresh bar cannot see the announcement
of a window whose program owns the title.

The file is only written once a shell has announced itself; entries are checked
against `/proc` before they are used, and entries whose process is gone are
dropped when the file is next written. A build with `-tags debug` (see the
`Makefile`) logs each window as it starts announcing a pid, and one with
`-tags trace` logs every sample.

## Contributing

Contributions are welcome! If you find a bug or have a feature request, please open an issue or PR.

## License

This project is licensed under the MIT License. See the [LICENSE](LICENSE) file for details.
