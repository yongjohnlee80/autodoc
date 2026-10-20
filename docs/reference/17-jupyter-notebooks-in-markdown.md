# Chapter 17: Notebooks in Markdown — The Jupyter Plugin

A Jupyter notebook is a good place to try code and a poor place to keep knowledge. Its `.ipynb` file is JSON, so `git diff` fills with base64 plots and execution counts. It is out of reach of your editor's search and links. And the prose around the code lives in cells that nothing else in your workspace can see.

AutoDoc's answer is the **lab note** (Chapter 4, §4.10): an ordinary Markdown note named `*.lab.md`, whose fenced code cells run in a Jupyter kernel through the **Jupyter plugin** (`autodoc-jupyter`, ADR 1792457255). The note is never edited by a run. Each cell's outputs are files in the note's companion folder, which AutoDoc's HTML export and `--gui`'s Rendered View draw under the cell. Opening an `.ipynb` turns it into a lab note beside it, and Export writes one back.

---

## 17.1 Lab Notes, Cells and Kernels

A lab note names its kernel in its front matter. Its **cells** are its fences in that kernel's language; every other fence (a `mermaid` diagram, a `csv` table, a `vega-lite` chart, `text`) stays content and never runs:

````markdown
---
kernel: python3
---

# Sign-ups

```python
import pandas as pd
df = pd.read_csv("signups.csv")
df.describe()
```

```mermaid
flowchart LR
  raw --> cleaned --> weekly
```
````

- **The kernel** is any kernelspec installed where Jupyter keeps them:
  - each folder of `$JUPYTER_PATH`;
  - your data folder (`$JUPYTER_DATA_DIR`, else `~/.local/share/jupyter`, or `~/Library/Jupyter` on macOS);
  - the active `$VIRTUAL_ENV`'s or `$CONDA_PREFIX`'s `share/jupyter`;
  - `/usr/local/share/jupyter`, then `/usr/share/jupyter`.

  Python's comes with ipykernel (`pip install ipykernel`, or your system's `python3-ipykernel`).
- **Which kernel runs a note:**
  - the one you picked on the card (Choose kernel…), for this session;
  - else the front matter's `kernel:`;
  - else the first Python kernel installed;
  - else the first of any.
- **A cell's language** is read as AutoDoc reads every fence: the first word of the info string, lower-cased. So ```` ```Python {.x} ```` is a `python` cell, and a kernelspec whose language is "Wolfram Language" runs ```` ```wolfram ```` fences.
- **Each note has its own kernel,** started in the note's folder, so a cell's relative paths (`signups.csv`) are the note's.

---

## 17.2 Installing the Plugin

The plugin needs an AutoDoc that speaks plugin protocol 9 (Chapter 15), which added `[open]`, the hook that hands it `.ipynb` files.

1. **Plugins › Browse the registry…** lists **Jupyter**. Choose it and confirm. You can also use **Plugins › Add from a git URL…** with `https://github.com/yongjohnlee80/autodoc-jupyter`.
2. AutoDoc asks before it runs anything, then builds the plugin with Go (`go build`, from its manifest's `[install]`).
3. Give it a letter on **SPC p**: this chapter writes `j` (AutoDoc assigns plugins no letter).

Its manifest declares:
- a non-modal card on the right;
- the document feed (the open note, its cursor and selection);
- a `DataFrame` table;
- `calls = ["file.open_path"]`;
- `[open] extensions = [".ipynb"]`, handled by its `import` command.

---

## 17.3 The Card and Its Commands

`SPC p j` opens the card. It shows:
- the note and its kernel, and the status;
- the last run's outputs as text (a plot as `[png output]`, a DataFrame as `[a table: N rows × M columns]`);
- the keys.

| Command | On the card | What it does |
|---|---|---|
| Run cell or selection | `SPC p j r` | The cell under the cursor (its fence lines included). With a selection, the selection alone: its outputs show on the card but are not kept, since they belong to no cell. |
| Run all cells | `SPC p j a` | Every cell in order, stopping at the first that fails. Then it removes the output folders no fence of the note has any more. |
| Interrupt kernel | `SPC p j x` | Stops the run: SIGINT to the kernel's process group, or `interrupt_request` for a kernel whose spec asks for messages. |
| Restart kernel | `SPC p j s` | Starts the kernel afresh, its variables gone. One restart at a time. |
| Choose kernel… | `SPC p j k` | Lists the installed kernels (↑↓, Enter; `q` leaves). The pick holds for this session; add `kernel: <name>` to the front matter to keep it. |
| Export to .ipynb | `SPC p j e` | Writes `nb.ipynb` beside `nb.lab.md`, never over an existing file. |
| Open an .ipynb as a lab note | (the `[open]` hook) | See §17.6. |

![How a cell runs, from the cursor to the drawn outputs](jupyter-cell-run-flow.mmd)

A run goes on a goroutine of its own, so the card still takes Interrupt while a long cell runs.

---

## 17.4 Outputs Beside the Note

A cell's outputs are files in `<note>.lab/out/<cell key>/`, named `01`, `02`, … in the order the kernel sent them.

- **The cell key** is a hash of the fence's language and its normalised code. Line endings are LF, and trailing newlines are dropped.
- So an **edited cell shows no outputs until it runs again**, and a fence of another language holding the very same text never shows them.
- Two cells with the same language and code share one folder.

![The files a kernel's outputs are kept as](jupyter-output-files.csv)

- **A run writes its files to a hidden folder** beside the cell's, then swaps it in. A reader sees the older run's outputs or the new run's, never a mix, and no file of an older run survives a rerun.
- A **`clear_output`** from the kernel drops what the run had kept so far, as Jupyter does.
- **Where they're drawn:**
  - the HTML preview and export draw each cell's outputs right after its fence, in name order;
  - `--gui`'s Rendered View draws them under the fence when the cursor is off it: text, every diagram and chart, tables, and PNG, JPEG and GIF images.
  - An SVG output shows in the HTML preview and export.
- Nothing is written into the note, so the note's `git diff` shows only what you wrote. Whether to commit `<note>.lab/out/` is your call.

---

## 17.5 DataFrames and the Table Panel

When a Python kernel starts, the plugin quietly turns on pandas' `display.html.table_schema`. A DataFrame then arrives as `application/vnd.dataresource+json`, and is kept twice:

- **as `NN.csv`**, with the index as its first column and a `null` cell empty, which the export and the Rendered View draw as a table;
- **in AutoDoc's table panel** (`DataFrame`), with each cell's kind (a number, text, null, a boolean). The panel is paged 200 rows at a time.

Without pandas, or with the option turned off again, a DataFrame is its `text/plain`, kept as `NN.txt`.

---

## 17.6 Opening and Exporting `.ipynb`

**Opening.** Open an `.ipynb` from the picker, **File › Open file…**, the explorer or `file.open_path`, and AutoDoc hands its path to the plugin's `import` command instead of opening its JSON as text. The plugin then:
- writes `nb.lab.md` beside `nb.ipynb`, with its kernel in the front matter;
- turns markdown cells into prose, code cells into fences in the kernel's language, and raw cells into `raw` fences;
- keeps each code cell's outputs under the cell's key, as a run would;
- opens the lab note (`file.open_path`).

When `nb.lab.md` already exists, nothing is written: it is the note to open, as it is.

**Exporting.** **Export to .ipynb** writes `nb.ipynb` (nbformat 4.5) beside the lab note:
- the prose between cells becomes markdown cells;
- each cell becomes a code cell, with the outputs its files hold;
- `raw` fences become raw cells.

Other fences stay in the markdown, as the note shows them. Export never writes over an existing notebook, so to export again, move the old one away first.

---

## 17.7 The Kernel's Lifecycle

- A note's kernel starts on its first run, and lives while the card is hidden: Esc closes a card's panel and the plugin runs on.
- **Restart** replaces it; a restart pressed while one runs is refused.
- Turning the plugin off, or quitting AutoDoc, stops every kernel within the host's two-second grace. A kernel still starting is abandoned, and the others are asked to shut down, and killed if they have not exited after a second.
- Each kernel is told its parent (`JPY_PARENT_PID`), so ipykernel exits with the plugin even though its process group is its own.
- The kernel is spoken to directly over ZeroMQ, in pure Go (`go-zeromq/zmq4`), with every message signed with HMAC-SHA256 and checked. There is no Python helper beside your kernel and no libzmq.

---

## 17.8 Hands-On Tutorial: A Weekly Sign-Ups Lab Note

Create `analysis/signups.lab.md`, with `signups.csv` (columns `week` and `signups`) beside it:

````markdown
---
kernel: python3
---

# Weekly sign-ups

```python
import pandas as pd
df = pd.read_csv("signups.csv")
df
```

```python
import matplotlib.pyplot as plt
df.plot(x="week", y="signups", kind="bar", legend=False)
plt.show()
```

```python
from IPython.display import display
display({"application/vnd.vegalite.v5+json": {
    "data": {"values": df.to_dict("records")},
    "mark": "bar",
    "encoding": {"x": {"field": "week", "type": "ordinal"},
                 "y": {"field": "signups", "type": "quantitative"}}}}, raw=True)
```
````

1. Open the note and press `SPC p j a`. The card says `starting Python 3 (ipykernel)…`, then `running 1 of 3…`, and ends with `ran 3 cells` and the time.
2. **The first cell's DataFrame** opens in the table panel, and is kept as `analysis/signups.lab/out/<key>/01.csv`.
3. **The second cell's plot** is kept as `01.png` in its own folder.
4. **The third cell's chart** is kept as `01.chart.json`, the chart below.
5. Switch to the Rendered View (`--gui`): each output is drawn under its cell. Export the note to HTML, and they follow their fences there too.

![The chart the tutorial's third cell keeps](lab-tutorial-signups.chart.json)

6. Change `"bar"` to `"line"` in the third cell. Its chart disappears from the Rendered View, since its key changed; press `SPC p j r` on it to draw the new one.
7. Press `SPC p j e` to write `analysis/signups.ipynb` for a colleague who uses Jupyter.

---

[← Previous: Chapter 16: AutoDB Live SQL Workbench](./16-autodb-live-sql-workbench.md) | [Index](./README.md) | [Next: Chapter 18: Neovim Integration & KB Migration →](./18-neovim-integration-and-kb-migration.md)
