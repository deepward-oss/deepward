# Terminal recording for the README. Render with `make demo-gif` from the
# repository root (needs vhs, plus Node.js and npm so `deepward fix` can
# update the lockfile). It runs the real binary on a temporary copy of
# testdata/projects/npm-app and never publishes anything.

Output docs/assets/demo.gif

Set Shell "bash"
Set FontSize 18
Set Width 1200
Set Height 680
Set Padding 24
Set Theme "Catppuccin Mocha"
Set TypingSpeed 45ms
Set WindowBar Colorful

Hide
Type "export PATH=$PWD/bin:$PATH PS1='$ ' && demo=$(mktemp -d) && cp -r testdata/projects/npm-app/. $demo && cd $demo && printf 'log:\n  progress: false\nfix:\n  kinds: [vulnerability]\n' > deepward.yaml && clear"
Enter
Sleep 500ms
Show

Type "deepward scan --fail-on high"
Sleep 600ms
Enter
Sleep 9s

Type "deepward fix | tail -n 13"
Sleep 600ms
Enter
Sleep 14s

Sleep 4s
