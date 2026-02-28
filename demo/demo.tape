# CodeDebate Demo Recording
Output demo/codedebate-demo.gif
Set Shell bash
Set FontSize 14
Set Width 900
Set Height 600
Set Theme "Catppuccin Mocha"
Set Padding 20
Set WindowBar Colorful
Set WindowBarSize 40

# Use PS1 to show clean prompt, then type the codedebate command
Require bash

# Set clean prompt
Hide
Type `export PS1="❯ "`
Enter
Sleep 200ms
Show

# Type the codedebate command (this is what users see)
Set TypingSpeed 40ms
Type "codedebate review 42 --reviewers claude,gpt4o,gemini"
Sleep 800ms
Enter

# Now secretly run the simulation
Hide
Type "bash demo/simulate.sh"
Enter
Show

Sleep 30s
