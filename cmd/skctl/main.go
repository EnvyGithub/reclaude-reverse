package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"text/tabwriter"

	"my-claude-proxy/internal/gateway"
)

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	skFile := "data/sk_keys.json"

	switch os.Args[1] {
	case "generate":
		fs := flag.NewFlagSet("generate", flag.ExitOnError)
		name := fs.String("name", "", "User name (required)")
		limit := fs.Int("daily-limit", 0, "Daily request limit (0=unlimited)")
		fs.Parse(os.Args[2:])

		if *name == "" {
			fmt.Println("Error: -name is required")
			os.Exit(1)
		}

		store, err := gateway.NewSKStore(skFile)
		if err != nil {
			log.Fatal(err)
		}

		sk, err := store.GenerateSK(*name, *limit)
		if err != nil {
			log.Fatal(err)
		}

		fmt.Println("✅ Generated new SK:")
		fmt.Printf("   Name:  %s\n", *name)
		fmt.Printf("   SK:    %s\n", sk)
		fmt.Printf("   Limit: %d/day\n", *limit)

	case "list":
		store, err := gateway.NewSKStore(skFile)
		if err != nil {
			log.Fatal(err)
		}

		keys := store.List()
		if len(keys) == 0 {
			fmt.Println("No SK keys found.")
			return
		}

		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "SK\tNAME\tENABLED\tLIMIT\tUSED")
		for _, k := range keys {
			fmt.Fprintf(w, "%s\t%s\t%v\t%d\t%d\n",
				k.SK[:16]+"...", k.Name, k.Enabled, k.DailyLimit, k.UsedToday)
		}
		w.Flush()

	case "disable":
		if len(os.Args) < 3 {
			fmt.Println("Usage: skctl disable <sk-token>")
			os.Exit(1)
		}
		sk := os.Args[2]
		store, err := gateway.NewSKStore(skFile)
		if err != nil {
			log.Fatal(err)
		}
		if err := store.SetEnabled(sk, false); err != nil {
			log.Fatal(err)
		}
		fmt.Printf("✅ Disabled SK: %s\n", sk)

	case "enable":
		if len(os.Args) < 3 {
			fmt.Println("Usage: skctl enable <sk-token>")
			os.Exit(1)
		}
		sk := os.Args[2]
		store, err := gateway.NewSKStore(skFile)
		if err != nil {
			log.Fatal(err)
		}
		if err := store.SetEnabled(sk, true); err != nil {
			log.Fatal(err)
		}
		fmt.Printf("✅ Enabled SK: %s\n", sk)

	case "stats":
		data, err := os.ReadFile(skFile)
		if err != nil {
			log.Fatal(err)
		}
		var parsed json.RawMessage
		json.Unmarshal(data, &parsed)
		out, _ := json.MarshalIndent(parsed, "", "  ")
		fmt.Println(string(out))

	default:
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Println("skctl — Session Key management tool")
	fmt.Println()
	fmt.Println("Commands:")
	fmt.Println("  generate -name <name> [-daily-limit <n>]  Generate new SK")
	fmt.Println("  list                                      List all SK keys")
	fmt.Println("  disable <sk>                              Disable an SK")
	fmt.Println("  enable <sk>                               Enable an SK")
	fmt.Println("  stats                                     Show raw SK data")
}
