"""Will start Jasmin configuration console
"""

from jasmin.cli.home import Home

if __name__ == "__main__":
    app = Home()
    app.cmdloop()