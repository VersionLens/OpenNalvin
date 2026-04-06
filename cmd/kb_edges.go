package cmd

import (
	"fmt"

	"github.com/versionlens/OpenNalvin/internal/knowledge"
	"github.com/versionlens/OpenNalvin/internal/output"
	"github.com/spf13/cobra"
)

var kbEdgesCmd = &cobra.Command{
	Use:   "edges",
	Short: "Manage knowledge graph edges",
}

var (
	kbEdgesListNodeID   string
	kbEdgesListRelation string
	kbEdgesListLimit    int

	kbEdgesCreateSourceID   string
	kbEdgesCreateTargetID   string
	kbEdgesCreateRelation   string
	kbEdgesCreateAttributes string

	kbEdgesUpdateSourceID   string
	kbEdgesUpdateTargetID   string
	kbEdgesUpdateRelation   string
	kbEdgesUpdateAttributes string

	kbEdgesDeleteYes bool
)

var kbEdgesListCmd = &cobra.Command{
	Use:   "list",
	Short: "List knowledge edges",
	RunE: func(cmd *cobra.Command, args []string) error {
		store, closeFn, err := kbStore(cmd.Context())
		if err != nil {
			return err
		}
		defer closeFn()

		edges, err := store.ListEdges(cmd.Context(), knowledge.EdgeFilter{
			NodeID:   kbEdgesListNodeID,
			Relation: kbEdgesListRelation,
			Limit:    kbEdgesListLimit,
		})
		if err != nil {
			return err
		}

		w := output.FromContext(cmd.Context())
		if w.IsJSON() {
			return w.JSON(edges)
		}
		for _, edge := range edges {
			w.Line("%-36s  %s --[%s]--> %s", edge.ID, edge.SourceNodeID[:8], edge.Relation, edge.TargetNodeID[:8])
		}
		return nil
	},
}

var kbEdgesGetCmd = &cobra.Command{
	Use:   "get <edge-id>",
	Short: "Get a knowledge edge",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		store, closeFn, err := kbStore(cmd.Context())
		if err != nil {
			return err
		}
		defer closeFn()

		edge, err := store.GetEdge(cmd.Context(), args[0])
		if err != nil {
			return err
		}

		w := output.FromContext(cmd.Context())
		if w.IsJSON() {
			return w.JSON(edge)
		}
		w.Line("ID:       %s", edge.ID)
		w.Line("Source:   %s", edge.SourceNodeID)
		w.Line("Target:   %s", edge.TargetNodeID)
		w.Line("Relation: %s", edge.Relation)
		if edge.Attributes != "{}" {
			w.Line("Attrs:    %s", edge.Attributes)
		}
		w.Line("Created:  %s", edge.CreatedAt.Format("2006-01-02 15:04:05"))
		w.Line("Updated:  %s", edge.UpdatedAt.Format("2006-01-02 15:04:05"))
		return nil
	},
}

var kbEdgesCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create a knowledge edge",
	RunE: func(cmd *cobra.Command, args []string) error {
		store, closeFn, err := kbStore(cmd.Context())
		if err != nil {
			return err
		}
		defer closeFn()

		edge, err := store.CreateEdge(cmd.Context(), knowledge.EdgeInput{
			SourceNodeID: kbEdgesCreateSourceID,
			TargetNodeID: kbEdgesCreateTargetID,
			Relation:     kbEdgesCreateRelation,
			Attributes:   kbEdgesCreateAttributes,
		})
		if err != nil {
			return err
		}

		w := output.FromContext(cmd.Context())
		if w.IsJSON() {
			return w.JSON(edge)
		}
		w.Line("Created edge %s", edge.ID)
		return nil
	},
}

var kbEdgesUpdateCmd = &cobra.Command{
	Use:   "update <edge-id>",
	Short: "Update a knowledge edge",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		store, closeFn, err := kbStore(cmd.Context())
		if err != nil {
			return err
		}
		defer closeFn()

		edge, err := store.UpdateEdge(cmd.Context(), args[0], knowledge.EdgeInput{
			SourceNodeID: kbEdgesUpdateSourceID,
			TargetNodeID: kbEdgesUpdateTargetID,
			Relation:     kbEdgesUpdateRelation,
			Attributes:   kbEdgesUpdateAttributes,
		})
		if err != nil {
			return err
		}

		w := output.FromContext(cmd.Context())
		if w.IsJSON() {
			return w.JSON(edge)
		}
		w.Line("Updated edge %s", edge.ID)
		return nil
	},
}

var kbEdgesDeleteCmd = &cobra.Command{
	Use:   "delete <edge-id>",
	Short: "Delete a knowledge edge",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if !kbEdgesDeleteYes {
			return fmt.Errorf("pass --yes to confirm deletion")
		}

		store, closeFn, err := kbStore(cmd.Context())
		if err != nil {
			return err
		}
		defer closeFn()

		if err := store.DeleteEdge(cmd.Context(), args[0]); err != nil {
			return err
		}

		w := output.FromContext(cmd.Context())
		if !w.IsJSON() {
			w.Line("Deleted edge %s", args[0])
		}
		return nil
	},
}

func init() {
	kbCmd.AddCommand(kbEdgesCmd)

	kbEdgesListCmd.Flags().StringVar(&kbEdgesListNodeID, "node-id", "", "filter by node ID")
	kbEdgesListCmd.Flags().StringVar(&kbEdgesListRelation, "relation", "", "filter by relation")
	kbEdgesListCmd.Flags().IntVarP(&kbEdgesListLimit, "limit", "n", 50, "max results")
	kbEdgesCmd.AddCommand(kbEdgesListCmd)

	kbEdgesCmd.AddCommand(kbEdgesGetCmd)

	kbEdgesCreateCmd.Flags().StringVar(&kbEdgesCreateSourceID, "source-id", "", "source node ID (required)")
	kbEdgesCreateCmd.Flags().StringVar(&kbEdgesCreateTargetID, "target-id", "", "target node ID (required)")
	kbEdgesCreateCmd.Flags().StringVar(&kbEdgesCreateRelation, "relation", "", "relationship label (required)")
	kbEdgesCreateCmd.Flags().StringVar(&kbEdgesCreateAttributes, "attributes", "", "JSON attributes")
	_ = kbEdgesCreateCmd.MarkFlagRequired("source-id")
	_ = kbEdgesCreateCmd.MarkFlagRequired("target-id")
	_ = kbEdgesCreateCmd.MarkFlagRequired("relation")
	kbEdgesCmd.AddCommand(kbEdgesCreateCmd)

	kbEdgesUpdateCmd.Flags().StringVar(&kbEdgesUpdateSourceID, "source-id", "", "source node ID")
	kbEdgesUpdateCmd.Flags().StringVar(&kbEdgesUpdateTargetID, "target-id", "", "target node ID")
	kbEdgesUpdateCmd.Flags().StringVar(&kbEdgesUpdateRelation, "relation", "", "relationship label")
	kbEdgesUpdateCmd.Flags().StringVar(&kbEdgesUpdateAttributes, "attributes", "", "JSON attributes")
	kbEdgesCmd.AddCommand(kbEdgesUpdateCmd)

	kbEdgesDeleteCmd.Flags().BoolVar(&kbEdgesDeleteYes, "yes", false, "confirm deletion")
	kbEdgesCmd.AddCommand(kbEdgesDeleteCmd)
}
