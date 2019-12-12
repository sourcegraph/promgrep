# promgrep

### Intro

`promgrep` is a small utility to search for the locations in Go code where
 [Prometheus](https://prometheus.io/) metrics are declared.

### Usage

Run `promgrep` in the root directory of your Go project
 (where your `go.mod` file is).
 
#### Without args
 
```shell script
promgrep
```

`promgrep` will list the locations of all the metric declarations together
with the metric names and help strings.

#### With one argument: name of a metric

```shell script
promgrep some:metric:name
```

`promgrep` will search for the location of that particular metric declaration.

### Matching

`promgrep` is doing static analysis and therefore can only deduce values of arguments
to metric construction function calls if those arguments are basic literals.

For cases where arguments are not derived it uses partial matching and 
assigns a relative score to each partial match (in the range [0..100] with 
0 meaning no match and 100 meaning exact match).

The matching function also accepts partial metric names to search for, so you
can run 

```shell script
promgrep some:partial:metric:name
```

and it will list all metric declarations that contain this partial name. 

### Output

The code locations in the `promgrep` output are of the form

```
relative_path_to_file:line_number
```

and should be clickable in Emacs buffers, Goland terminals and other tools
that parse format and make it navigable. Clicking on an output line should get you to
the place in code where the declaration is.
