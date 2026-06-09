/*
Copyright © 2025 YAUHEN SHULITSKI
*/
package main

import (
	_ "time/tzdata" // IANA zones for Open-Meteo timezone=auto parsing on tzdata-less hosts

	"weather/cmd"
)

func main() {
	cmd.Execute()
}
